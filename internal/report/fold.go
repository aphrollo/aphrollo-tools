package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// cell is the finest grain the fold keeps: what one rule did on one lane, in one
// language, on one day. Every section is a regrouping of the cells, so a new
// grouping is a new key function and no new fold, and a derived number (a
// share, a rate, the seconds lost) is computed at read time and never stored.
type cell struct{ repo, lane, rule, lang, day string }

// counts is what a cell holds. waited and background split the gate's seconds:
// the time the agent stood waiting on the gate, and the time the gate ran while
// the agent kept working.
type counts struct {
	denies, overrides, refusals, notTested, waived int
	waited, background                             float64
	seqs, wrongSeqs                                []int64
}

// lastDeny is the most recent deny of a lane, for the override that follows it.
type lastDeny struct {
	at       time.Time
	rule     string
	consumed bool
}

type fold struct {
	repo      string
	cells     map[cell]*counts
	standdown map[string]*Standdown
	standSeqs map[string][]int64
	escapes   map[[2]string]*EscapeRow
	escSeqs   map[[2]string][]int64
	falsePos  int
	lines     map[string]*GateLine
	lineSeqs  map[string][]int64
	last      map[string]*lastDeny
}

func newFold() *fold {
	return &fold{
		cells:     map[cell]*counts{},
		standdown: map[string]*Standdown{}, standSeqs: map[string][]int64{},
		escapes: map[[2]string]*EscapeRow{}, escSeqs: map[[2]string][]int64{},
		lines: map[string]*GateLine{}, lineSeqs: map[string][]int64{}, last: map[string]*lastDeny{},
	}
}

// at is the cell of event e for rule, made when it is new.
func (f *fold) at(e stamped, rule string) *counts {
	k := cell{repo: f.repo, lane: e.Lane, rule: rule, lang: langOf(e), day: e.at.UTC().Format("2006-01-02")}
	c := f.cells[k]
	if c == nil {
		c = &counts{}
		f.cells[k] = c
	}
	return c
}

// langExt maps a file extension to the language the A/B groups by.
var langExt = map[string]string{".go": "go", ".py": "python", ".ts": "typescript", ".tsx": "typescript", ".js": "typescript", ".jsx": "typescript", ".rs": "rust"}

// langOf is the language an event names, from its lang detail or its file's extension.
func langOf(e stamped) string {
	if l := e.Detail["lang"]; l != "" {
		return l
	}
	file := e.Detail["file"]
	if i := strings.LastIndexByte(file, '.'); i >= 0 {
		return langExt[strings.ToLower(file[i:])]
	}
	return ""
}

// backgroundVerdict says the gate ran while the agent kept working: a deferred
// or queued run. The log carries no wait flag, so the verdict is the evidence.
func backgroundVerdict(e stamped) bool {
	v := strings.ToLower(e.Verdict)
	return e.Kind == "queue" || strings.Contains(v, "deferred") || strings.Contains(v, "queued")
}

func (c *counts) addSecs(e stamped) {
	if backgroundVerdict(e) {
		c.background += e.Secs
		return
	}
	c.waited += e.Secs
}

// see folds one event; counted is false for an event before the window, which
// still tells the fold which deny an override answers.
func (f *fold) see(e stamped, counted bool) {
	switch e.Kind {
	case "deny":
		rule := denyRule(e)
		f.last[e.Lane] = &lastDeny{at: e.at, rule: rule}
		if counted {
			c := f.at(e, rule)
			c.denies++
			c.seqs = append(c.seqs, e.Seq)
			c.wrongSeqs = append(c.wrongSeqs, e.Seq)
		}
	case "override":
		if !counted || isAllowedRerun(e) {
			return
		}
		rule := e.Detail["override"]
		if rule == "" {
			rule = e.Verdict
		}
		if d := f.last[e.Lane]; d != nil && e.at.Sub(d.at) <= measure.WrongBlockWindow {
			rule = d.rule
			if !d.consumed {
				d.consumed = true
				w := f.at(e, rule)
				w.waived++
				w.wrongSeqs = append(w.wrongSeqs, e.Seq)
			}
		}
		c := f.at(e, rule)
		c.overrides++
		c.seqs = append(c.seqs, e.Seq)
	case "run.result":
		if counted && e.Detail["result"] == "not-tested" {
			cause := e.Detail["cause"]
			if cause == "" {
				cause = "unknown"
			}
			c := f.at(e, "run:"+cause)
			c.notTested++
			c.seqs = append(c.seqs, e.Seq)
		}
	case "escape":
		if counted {
			f.escape(e)
		}
	}
	if counted && gateKinds[e.Kind] {
		f.gateLine(e)
	}
}

// gateKinds are the events that carry a gate line: a stage and its verdict.
var gateKinds = map[string]bool{"commit_gate": true, "merge_gate": true, "commit_gate_result": true, "gate": true, "stage.timing": true}

// refusalKinds are the gate lines whose verdict can be a refusal: a stage's
// own red is a test result, not friction.
var refusalKinds = map[string]bool{"commit_gate": true, "merge_gate": true, "commit_gate_result": true}

// refusalSuffixes and refusalExact name a commit or merge gate verdict that refused.
var (
	refusalSuffixes = []string{"-blocked", "-rejected", "-refused"}
	refusalExact    = map[string]bool{"still-red": true, "violated": true, "blocked": true}
)

// notTestedParts name a verdict whose work did not run, whatever its case:
// TIMEOUT, SKIPPED, QUEUED-SKIPPED, NOT RUN, NOT MEASURED and the lower-case
// verdicts of the gate (suites-not-run, mutants-unmeasured, lint-timeout).
// Where nothing existed to measure, nothing went untested.
var (
	notTestedParts  = []string{"timeout", "skipped", "not run", "not-run", "not measured", "not-measured", "unmeasured", "inconclusive"}
	notTestedExcept = "nothing-to-measure"
)

// verdictHead is the verdict without its detail: "lint-blocked:3 findings" is "lint-blocked".
func verdictHead(v string) string {
	v, _, _ = strings.Cut(v, ":")
	v, _, _ = strings.Cut(v, " (")
	return strings.TrimSpace(v)
}

func isRefusal(head string) bool {
	if refusalExact[head] {
		return true
	}
	for _, s := range refusalSuffixes {
		if strings.HasSuffix(head, s) {
			return true
		}
	}
	return false
}

// isNotTested reads the verdict's head only: a detail ("unmeasured=0") or a path
// in it is not the verdict.
func isNotTested(verdict string) bool {
	if strings.Contains(strings.ToLower(verdict), notTestedExcept) {
		return false
	}
	l := strings.ToLower(verdictHead(verdict))
	for _, p := range notTestedParts {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

func (f *fold) gateLine(e stamped) {
	head := verdictHead(e.Verdict)
	if head == "" {
		return
	}
	if strings.HasPrefix(head, "standdown-") {
		m := strings.TrimPrefix(e.Verdict, "standdown-")
		s := f.standdown[m]
		if s == nil {
			s = &Standdown{Matcher: m}
			f.standdown[m] = s
		}
		s.N++
		f.standSeqs[m] = append(f.standSeqs[m], e.Seq)
		return
	}
	key := "gate:" + head
	switch {
	case refusalKinds[e.Kind] && isRefusal(head):
		c := f.at(e, key)
		c.refusals++
		c.addSecs(e)
		c.seqs = append(c.seqs, e.Seq)
	case isNotTested(e.Verdict):
		c := f.at(e, key)
		c.notTested++
		c.addSecs(e)
		c.seqs = append(c.seqs, e.Seq)
	}
	// The text a session reads: the line's verdict and the command it names.
	name := e.Kind + ":" + e.Stage + ":" + head
	l := f.lines[name]
	if l == nil {
		l = &GateLine{Name: name}
		f.lines[name] = l
	}
	l.N++
	l.Bytes += len(e.Verdict) + len(e.Cmd)
	f.lineSeqs[name] = append(f.lineSeqs[name], e.Seq)
}

func (f *fold) escape(e stamped) {
	if e.Verdict == "false-positive" {
		f.falsePos++
		return
	}
	class := e.Detail["class"]
	switch {
	case e.Verdict == "outside-merge":
		class = "outside-merge"
	case class == "":
		class = "unclassified"
	}
	key := [2]string{class, caughtBy(class, e.Detail["check"], e.Detail["from_ci"])}
	r := f.escapes[key]
	if r == nil {
		r = &EscapeRow{Class: key[0], Caught: key[1]}
		f.escapes[key] = r
	}
	r.N++
	f.escSeqs[key] = append(f.escSeqs[key], e.Seq)
}

// caughtBy is the stage that should have caught an escape: what its class says
// (the classes of tdd/escape), else the CI job that found it after a local
// green, else the stage the recorder named.
func caughtBy(class, check, fromCI string) string {
	switch {
	case class == "canary":
		return "test isolation (gitworld)"
	case class == "disagreement":
		return "commit gate (an earlier stage passed what the merge gate refused)"
	case class == "outside-merge":
		return "merge verb (the merge skipped it)"
	case fromCI != "":
		return "local test gate (CI job " + fromCI + " caught it)"
	case check != "":
		return "the " + check + " stage"
	}
	return "not recorded (pass --check to `aphrollo gate escape record`)"
}

// denyRule is the rule a deny came from: its detail, else its verdict (the
// older writer put the rule there, after "pretooluse-denied:").
func denyRule(e stamped) string {
	if rule := e.Detail["rule"]; rule != "" {
		return rule
	}
	if rule := strings.TrimPrefix(e.Verdict, "pretooluse-denied:"); rule != "" {
		return rule
	}
	return "unknown"
}

// isAllowedRerun is the old spelling of an allowed narrowed rerun: the hook let
// the run through, so nothing was waived.
func isAllowedRerun(e stamped) bool {
	return e.Detail["override"] == "override-bash-narrowed" || e.Verdict == "override-bash-narrowed"
}

// results turns the tallies into rows, the most costly first, ties by name.
func (f *fold) results() ([]Friction, []WrongBlock, []Standdown, Escapes) {
	byRule := map[string]*Friction{}
	seqs := map[string][]int64{}
	wrong := map[string]*WrongBlock{}
	wrongSeqs := map[string][]int64{}
	// The regrouping: every row of a section is the cells summed by one key.
	for k, c := range f.cells {
		fr := byRule[k.rule]
		if fr == nil {
			fr = &Friction{Rule: k.rule}
			byRule[k.rule] = fr
		}
		fr.Denies += c.denies
		fr.Overrides += c.overrides
		fr.Refusals += c.refusals
		fr.NotTested += c.notTested
		fr.SecsLost += c.waited
		fr.SecsBackground += c.background
		seqs[k.rule] = append(seqs[k.rule], c.seqs...)
		if c.denies > 0 || c.waived > 0 {
			w := wrong[k.rule]
			if w == nil {
				w = &WrongBlock{Rule: k.rule}
				wrong[k.rule] = w
			}
			w.Denies += c.denies
			w.Waived += c.waived
			wrongSeqs[k.rule] = append(wrongSeqs[k.rule], c.wrongSeqs...)
		}
	}
	var fr []Friction
	for rule, t := range byRule {
		t.Refs = makeRefs(seqs[rule])
		fr = append(fr, *t)
	}
	sort.Slice(fr, func(i, j int) bool {
		if fr[i].total() != fr[j].total() {
			return fr[i].total() > fr[j].total()
		}
		return fr[i].Rule < fr[j].Rule
	})
	var wb []WrongBlock
	for rule, t := range wrong {
		t.Refs = makeRefs(wrongSeqs[rule])
		if t.Denies > 0 {
			t.Rate = fmt.Sprintf("%.0f%%", float64(t.Waived)/float64(t.Denies)*100)
		}
		wb = append(wb, *t)
	}
	sort.Slice(wb, func(i, j int) bool {
		if wb[i].Waived != wb[j].Waived {
			return wb[i].Waived > wb[j].Waived
		}
		if wb[i].Denies != wb[j].Denies {
			return wb[i].Denies > wb[j].Denies
		}
		return wb[i].Rule < wb[j].Rule
	})
	var sd []Standdown
	for m, s := range f.standdown {
		s.Refs = makeRefs(f.standSeqs[m])
		sd = append(sd, *s)
	}
	sort.Slice(sd, func(i, j int) bool {
		if sd[i].N != sd[j].N {
			return sd[i].N > sd[j].N
		}
		return sd[i].Matcher < sd[j].Matcher
	})
	x := Escapes{FalsePositives: f.falsePos}
	for k, r := range f.escapes {
		r.Refs = makeRefs(f.escSeqs[k])
		x.Rows = append(x.Rows, *r)
	}
	sort.Slice(x.Rows, func(i, j int) bool {
		if x.Rows[i].N != x.Rows[j].N {
			return x.Rows[i].N > x.Rows[j].N
		}
		if x.Rows[i].Class != x.Rows[j].Class {
			return x.Rows[i].Class < x.Rows[j].Class
		}
		return x.Rows[i].Caught < x.Rows[j].Caught
	})
	return fr, wb, sd, x
}

// biggestLinesN is how many gate lines the report names.
const biggestLinesN = 5

// biggestLines are the gate lines that cost the most tokens over the window:
// the recorded verdict and command of each line, by stage and verdict head.
// The hook's own prose is not in the log, so this is a floor, not the bill.
func (f *fold) biggestLines() []GateLine {
	var out []GateLine
	for name, l := range f.lines {
		l.Tokens = measure.Tokens(l.Bytes)
		l.Refs = makeRefs(f.lineSeqs[name])
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > biggestLinesN {
		out = out[:biggestLinesN]
	}
	return out
}
