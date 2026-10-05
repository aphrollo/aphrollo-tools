// Package shadow records what the trellis kernel would have decided beside what
// a live aphrollo hook did (docs/trellis-architecture.md §12, F28 to F29). It is
// record-only: nothing here returns a decision to a hook, a record that cannot be
// written inside the budget is dropped, and no record carries a command's text.
//
// The stateless rules are the ones the kernel decides from facts the hook already
// holds. Each is asked of kernel.Decide with an empty lane State and no units, so a
// rule that starts reading state would skew the data: the test of this package asks
// every such rule under populated states and fails if the answer moves.
//
// Red→green and stop-red read a unit's state, and are asked of the lane's real
// record instead (world.go): the engine loads it from the store, the adapters
// (payload.go) build the question from the hook's payload, a unit is named per edited
// file (unit.go), Covered says whether its newest edit is on a tree a green run of
// it measured (covered.go), and every finished run is folded into the record
// (World.FoldRun) after the hook has answered. All of it runs inside the same
// Budget as the other records; what does not finish is an unjudged record with its
// cause, never a guess.
//
// A hook writes its records after it has answered, and does wait for them: up to
// Budget, past which the record is dropped. A PostToolUse hook queues the run it
// harvested (QueueRun) while it builds its answer, and Flush writes the queue
// after the answer, under the same wait. TakeWaited reports that wait so the
// hook's own timing can leave it out.
//
// A record's relation says how the two decisions compare, ranked allow below
// guide or warn below block: agree, trellis-stricter (a would-be block), or
// trellis-softer (aphrollo denies where trellis would only guide). A
// disagreement is not a wrong block: those and the catches come only from what
// the log shows afterwards (measure.ComputeShadow).
package shadow

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// Action is what a live aphrollo hook did about a fact.
type Action string

const (
	Block Action = "block"
	Warn  Action = "warn"
	Allow Action = "allow"
)

// Relation is how trellis's decision compares to aphrollo's.
type Relation string

const (
	Agree           Relation = "agree"
	TrellisStricter Relation = "trellis-stricter"
	TrellisSofter   Relation = "trellis-softer"
	// VerdictMismatch is a run whose verdict class trellis reads differently from
	// aphrollo's line.
	VerdictMismatch Relation = "verdict-mismatch"
	// Unjudged is a run no verdict was made of: aphrollo's line for it names none, or
	// the kernel's reading of it is no verdict. Counted apart, never as agreement.
	Unjudged Relation = "unjudged"
	// NotComparable is a run aphrollo classed in a way the kernel's verdict input
	// cannot express (a bogus red): counted apart, never as a mismatch.
	NotComparable Relation = "not-comparable"
)

// Hooks a record can come from.
const (
	HookPre = "pretooluse"
	HookRun = "posttooluse-run"
)

// Budget is how long a hook waits for a record to be built and written. Past it
// the record is skipped: the hook never waits longer for a measurement.
var Budget = 150 * time.Millisecond

// appendEvent is where a record is written, a seam for the tests of this package.
var appendEvent = core.AppendEvent

// Fact is one observation a live hook made about a rule the kernel decides from
// the facts of the call alone: the rule's id, aphrollo's own name for it, what
// aphrollo did, and the event the kernel is asked about.
type Fact struct {
	Rule     string
	LiveRule string
	Actual   Action
	Event    kernel.Event
	Config   kernel.Config
	Root     string // the checkout the fact is about, "" for the call's own
}

// Record is the outcome of judging one fact.
type Record struct {
	Hook     string
	Rule     string
	LiveRule string
	Trellis  string // block, warn, guide or allow
	Actual   string // block, warn, allow; for a run, aphrollo's verdict word
	Relation Relation
	HeldOut  bool // the kernel put this fire in the holdout arm
	Primary  bool // the fact is about the primary checkout, which has no lane of its own to join outcomes by
	Guide    string
	Unit     string // the unit the record is about, for the rules that read a unit's state
	UnitPkg  string // a Go unit's package relative to its module root, what a proof's patterns are relative to
	Root     string // the checkout the record is about, "" for the call's own
	// For a run: the verdict classes each side read, and the cause.
	TrellisVerdict, ActualVerdict, Cause, ActualCause string
}

func tool(t kernel.Tool, cmds kernel.Cmd) kernel.Event {
	return kernel.Event{Kind: kernel.KindPreTool, Claude: true, Tool: t, Cmds: cmds}
}

// PrimaryWrite is a write that lands in the primary checkout on trunk.
func PrimaryWrite(t kernel.Tool, actual Action) Fact {
	e := tool(t, 0)
	if t == kernel.ToolBash {
		e.Cmds = kernel.CmdWrite
	}
	e.Target, e.Lane = kernel.PathPrimary, kernel.TrunkLane
	return Fact{Rule: "primary-write", LiveRule: "primary-checkout", Actual: actual, Event: e}
}

// Discard is a command that discards uncommitted work.
func Discard(actual Action) Fact {
	return Fact{Rule: "discard-work", LiveRule: "discard-bash", Actual: actual, Event: tool(kernel.ToolBash, kernel.CmdDiscard)}
}

// Outward is a call that does by hand what a verb does.
func Outward(live string, actual Action) Fact {
	return Fact{Rule: "bypass-verb", LiveRule: live, Actual: actual, Event: tool(kernel.ToolBash, kernel.CmdOutward)}
}

// Attribution is attribution in an undercover repo.
func Attribution(actual Action) Fact {
	e := tool(kernel.ToolBash, 0)
	e.Attribution = true
	return Fact{Rule: "attribution", LiveRule: "undercover", Actual: actual, Event: e, Config: kernel.Config{Undercover: true}}
}

// Law is a law hit on an edit: a deny law's weight rose, or a warn law tripped.
func Law(law string, deny bool, actual Action) Fact {
	e := tool(kernel.ToolWrite, 0)
	e.LawHit, e.LawDeny = law, deny
	rule := "warn-law"
	if deny {
		rule = "deny-law-edit"
	}
	return Fact{Rule: rule, LiveRule: law, Actual: actual, Event: e}
}

// Rerun is a whole-suite run the run already covered.
func Rerun(actual Action) Fact {
	return Fact{Rule: "rerun-suite", LiveRule: "bash-whole-suite", Actual: actual, Event: tool(kernel.ToolBash, kernel.CmdRerun)}
}

// rank orders the two sides' strictness: allow, then guide or warn, then block.
func rank(a string) int {
	switch a {
	case "block":
		return 2
	case "warn", "guide":
		return 1
	}
	return 0
}

func trellisWord(d kernel.Decision) string {
	switch {
	case d.Outcome == kernel.OutcomeDeny:
		return "block"
	case d.Outcome == kernel.OutcomeGuide && d.WouldDeny:
		return "warn"
	case d.Outcome == kernel.OutcomeGuide:
		return "guide"
	}
	return "allow"
}

// decide asks the kernel about the fact under the given state. Judge passes
// none; the tests pass others to prove no shadowed rule reads it.
func decide(f Fact, lane string, st kernel.State, us kernel.Units) kernel.Decision {
	e := f.Event
	if e.Lane == "" {
		e.Lane = lane
	}
	return kernel.Decide(st, us, e, f.Config)
}

// Judge asks the kernel what it would decide for the fact, on lane, and compares
// it to what aphrollo did. The kernel sees an empty lane State and no units.
func Judge(f Fact, lane string) Record {
	d := decide(f, lane, kernel.State{}, nil)
	r := recordOf(HookPre, f.Rule, f.LiveRule, d, f.Actual)
	r.Primary = f.Event.Target == kernel.PathPrimary
	return r
}

// recordOf compares a kernel decision to what aphrollo did about the same rule.
func recordOf(hook, rule, live string, d kernel.Decision, actual Action) Record {
	t := trellisWord(d)
	rel := Agree
	switch a, b := rank(t), rank(string(actual)); {
	case a > b:
		rel = TrellisStricter
	case a < b:
		rel = TrellisSofter
	}
	return Record{Hook: hook, Rule: rule, LiveRule: live, Trellis: t, Actual: string(actual),
		Relation: rel, HeldOut: d.HeldOut}
}

// RunFact is one finished run: aphrollo's verdict word for it, and the verdict
// class and cause the kernel's own reading of the same run gives.
type RunFact struct {
	Word    string
	Verdict kernel.Verdict
	Cause   string
}

// ClassOf is the kernel verdict class of aphrollo's own verdict word and the
// cause of a run that did not test. ok is false for a word that names no run
// verdict, which is then not recorded.
func ClassOf(word string) (v kernel.Verdict, cause string, ok bool) {
	switch {
	case word == "red-missing-impl":
		return kernel.VerdictRedMissingImpl, "", true
	case word == "red-bogus":
		return kernel.VerdictRedBogus, "", true
	case word == "red" || word == "no-delta":
		return kernel.VerdictRed, "", true
	case word == "writing-test" || strings.HasPrefix(word, "green"):
		return kernel.VerdictGreen, "", true
	case word == "infra-failed":
		return kernel.VerdictNotTested, kernel.CauseInfra, true
	case strings.HasPrefix(word, "timeout"):
		return kernel.VerdictNotTested, kernel.CauseTimeout, true
	case strings.HasPrefix(word, "skipped"), word == "memory-skipped", word == "oom-killed", word == "no-tests-selected":
		return kernel.VerdictNotTested, kernel.CauseSkipped, true
	}
	return "", "", false
}

// JudgeRun compares the kernel's reading of a run with aphrollo's line, and
// records what the kernel would say of it: a guide for a run that did not test or
// was a bogus red, nothing for a green or a real red. A run is a fact, so the
// kernel cannot deny it. It reports false for an aphrollo word that is no verdict.
func JudgeRun(f RunFact, lane string) (Record, bool) {
	actual, actualCause, ok := ClassOf(f.Word)
	if !ok || f.Verdict == "" {
		return Record{}, false
	}
	d := kernel.Decide(kernel.State{}, nil, kernel.Event{Kind: kernel.KindRunResult, Lane: lane, Unit: "run",
		Tree: "t", Verdict: f.Verdict, Cause: f.Cause}, kernel.Config{})
	r := Record{Hook: HookRun, Rule: "run-verdict", Trellis: "allow", Actual: f.Word, Relation: Agree,
		TrellisVerdict: string(f.Verdict), ActualVerdict: string(actual), Cause: f.Cause}
	for _, fx := range d.Effects {
		if fx.Kind == kernel.EffectGuide {
			r.Guide, r.Trellis = fx.Detail, "guide"
		}
	}
	switch {
	case actual == kernel.VerdictRedBogus && verdictClass(f.Verdict) == kernel.VerdictRed:
		// phaseVerdict reads every failing run as a red and cannot say bogus.
		r.Relation = NotComparable
	case verdictClass(f.Verdict) != verdictClass(actual) || (f.Verdict == kernel.VerdictNotTested && f.Cause != actualCause):
		r.Relation = VerdictMismatch
	}
	r.ActualCause = actualCause
	return r, true
}

// verdictClass folds the two real reds into one: the kernel treats a red and a
// red-missing-impl alike (a real red), and a bogus red as its own class.
func verdictClass(v kernel.Verdict) kernel.Verdict {
	if v == kernel.VerdictRedMissingImpl {
		return kernel.VerdictRed
	}
	return v
}

// Source is where a record came from: the repo root it is filed under, the
// actor, and the tree or lane key a later fold joins it to outcomes by.
type Source struct {
	Root, Actor, Key string
}

// event is the record as the event log carries it: kind shadow, metadata only.
func (r Record) event(s Source, lane string) core.Event {
	d := map[string]string{
		"hook": r.Hook, "rule": r.Rule, "trellis": r.Trellis, "aphrollo": r.Actual, "relation": string(r.Relation),
	}
	set := func(k, v string) {
		if v != "" {
			d[k] = v
		}
	}
	set("live_rule", r.LiveRule)
	set("wt", s.Root)
	set("key", s.Key)
	set("unit", r.Unit)
	set("unit_pkg", r.UnitPkg)
	set("guide", r.Guide)
	set("trellis_verdict", r.TrellisVerdict)
	set("aphrollo_verdict", r.ActualVerdict)
	set("cause", r.Cause)
	set("aphrollo_cause", r.ActualCause)
	if r.Primary {
		d["primary"] = "true"
	}
	if r.HeldOut {
		d["held_out"] = "true"
	}
	return core.Event{Kind: core.KindShadow, Root: s.Root, Actor: s.Actor, Lane: lane, Detail: d}
}

// Enabled turns recording on. It is a seam for tests, which prove a hook's answer
// is the same with it off.
var Enabled = true

// waited is how long hooks have waited on records in this process.
var waited atomic.Int64

// TakeWaited is the time this process has spent waiting for shadow records since
// the last call, so a caller that times the hook can leave it out: the hook does
// wait up to Budget for its record after it has answered.
func TakeWaited() time.Duration { return time.Duration(waited.Swap(0)) }

// boundedUntil runs fn and waits for it no longer than Budget. fn runs on its own
// goroutine, so a hook that outruns the budget moves on; a panic in it is
// dropped, never raised into the hook. The wait is counted for TakeWaited. ctx is
// cancelled when the budget is spent, which ends every wait fn makes on it (a
// store lock, a load). It reports whether fn finished inside the budget. With
// recording off it runs nothing and reports true.
func boundedUntil(fn func(ctx context.Context)) bool {
	if !Enabled {
		return true
	}
	start := time.Now()
	defer func() { waited.Add(int64(time.Since(start))) }()
	ctx, cancel := context.WithTimeout(context.Background(), Budget)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		fn(ctx)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// RecordFacts writes one shadow event for each fact of a PreToolUse call. The
// facts are asked for inside the budget with the writes, so a fact that needs a
// look at the repository is not paid for outside it. A fact names its own root
// when it is about another checkout than the call's. It never returns anything to
// the hook.
func RecordFacts(s Source, facts func() []Fact) { RecordFactsAnd(s, facts, nil) }

// RecordFactsAnd is RecordFacts with the red→green steps of the same call in the
// same budget: the call waits once, however many records it makes. A step that did
// not finish inside the budget is written as unjudged, with its cause, and counted
// in Overruns; it is never guessed.
func RecordFactsAnd(s Source, facts func() []Fact, steps []Step) {
	w := &window{}
	finished := boundedUntil(func(ctx context.Context) {
		for _, f := range facts() {
			fs := s
			if f.Root != "" {
				fs.Root = f.Root
			}
			lane := core.LaneOf(fs.Root)
			if !w.write(Judge(f, lane).event(fs, lane)) {
				return
			}
		}
		runSteps(ctx, w, steps)
	})
	if !finished {
		settle(w, steps)
	}
}

// RedGreenSteps is the step of a PreToolUse call that shadows red→green over the
// files it is about to write. It makes none for a call that writes no code or
// test file.
func RedGreenSteps(wd World, s Source, p Payload, files []string) []Step {
	if !hasWrites(files) {
		return nil
	}
	return []Step{{
		run: func(ctx context.Context) []core.Event {
			recs := wd.RedGreen(ctx, p, files)
			evs := make([]core.Event, 0, len(recs))
			for _, r := range recs {
				rs := s
				if r.Root != "" {
					rs.Root = r.Root
				}
				evs = append(evs, r.event(rs, wd.Lane(rs.Root)))
			}
			return evs
		},
		skip: func(cause string) core.Event {
			return unjudged(HookPre, RuleRedGreen, "", cause).event(s, wd.Lane(s.Root))
		},
	}}
}

// RecordStop writes the stop-red shadow record of a Stop or SubagentStop, whatever
// the live check did, inside one budget.
func RecordStop(wd World, hook string, s Source, p Payload, f StopFacts) {
	RecordFactsAnd(s, func() []Fact { return nil }, []Step{{
		run: func(ctx context.Context) []core.Event {
			r, ok := wd.Stop(ctx, hook, p, s.Root, f)
			if !ok {
				return nil
			}
			return []core.Event{r.event(s, wd.Lane(s.Root))}
		},
		skip: func(cause string) core.Event {
			return unjudged(hook, RuleStopRed, "", cause).event(s, wd.Lane(s.Root))
		},
	}})
}

type queuedRun struct {
	src  Source
	fact func() (RunFact, bool)
}

type queuedEdit struct {
	src  Source
	w    World
	edit EditFold
}

type queuedFold struct {
	src  Source
	w    World
	fold Fold
}

var queue struct {
	sync.Mutex
	runs  []queuedRun
	edits []queuedEdit
	folds []queuedFold
}

// QueueRun holds a finished run to be recorded by Flush, after the hook has
// answered. fact must read nothing: the caller has the run's facts already.
func QueueRun(s Source, fact func() (RunFact, bool)) {
	queue.Lock()
	queue.runs = append(queue.runs, queuedRun{s, fact})
	queue.Unlock()
}

// QueueEditFold holds an edit the hook has made to be folded into its lane's record
// by Flush, after the hook has answered and ahead of any run folded in the same
// flush, so a run finds the edits it judged already there.
func QueueEditFold(s Source, w World, f EditFold) {
	queue.Lock()
	queue.edits = append(queue.edits, queuedEdit{s, w, f})
	queue.Unlock()
}

// QueueFold holds a finished run to be folded into its lane's record by Flush,
// after the hook has answered, in the same budget as the run's own record.
func QueueFold(s Source, w World, f Fold) {
	queue.Lock()
	queue.folds = append(queue.folds, queuedFold{s, w, f})
	queue.Unlock()
}

// Flush writes one shadow event for each queued run, agreeing ones too, inside
// one budget, and empties the queue. A run whose fact reports false, or whose
// word names no verdict, is written as unjudged. The queued folds follow in the
// same budget: a fold that cannot be made (no tree key, no lane, the budget) is
// one unjudged record naming the cause, and one that is made writes none, for the
// lane record it changed is its own trace.
func Flush() {
	queue.Lock()
	runs, edits, folds := queue.runs, queue.edits, queue.folds
	queue.runs, queue.edits, queue.folds = nil, nil, nil
	queue.Unlock()
	if len(runs) == 0 && len(edits) == 0 && len(folds) == 0 {
		return
	}
	steps := make([]Step, 0, len(edits)+len(folds))
	for _, q := range edits {
		steps = append(steps, editStep(q))
	}
	for _, q := range folds {
		steps = append(steps, foldStep(q))
	}
	w := &window{}
	finished := boundedUntil(func(ctx context.Context) {
		for _, q := range runs {
			f, ok := q.fact()
			lane := core.LaneOf(q.src.Root)
			r, judged := Record{}, false
			if ok {
				r, judged = JudgeRun(f, lane)
			}
			if !judged {
				r = unjudgedRun(f.Word)
			}
			appendEvent(r.event(q.src, lane))
		}
		runSteps(ctx, w, steps)
	})
	if !finished {
		settle(w, steps)
	}
}

func editStep(q queuedEdit) Step {
	lane := func() string { return core.LaneOf(q.src.Root) }
	return Step{
		run: func(ctx context.Context) []core.Event {
			if cause := q.w.FoldEdit(ctx, q.edit); cause != "" {
				return []core.Event{unjudged(HookFold, RuleFold, "", cause).event(q.src, lane())}
			}
			return nil
		},
		skip: func(cause string) core.Event {
			return unjudged(HookFold, RuleFold, "", cause).event(q.src, lane())
		},
	}
}

func foldStep(q queuedFold) Step {
	lane := func() string { return core.LaneOf(q.src.Root) }
	return Step{
		run: func(ctx context.Context) []core.Event {
			if cause := q.w.FoldRun(ctx, q.fold); cause != "" {
				return []core.Event{unjudged(HookFold, RuleFold, "", cause).event(q.src, lane())}
			}
			return nil
		},
		skip: func(cause string) core.Event {
			return unjudged(HookFold, RuleFold, "", cause).event(q.src, lane())
		},
	}
}

// unjudgedRun is the record of a run nothing could be judged of.
func unjudgedRun(word string) Record {
	return Record{Hook: HookRun, Rule: "run-verdict", Actual: word, Relation: Unjudged}
}
