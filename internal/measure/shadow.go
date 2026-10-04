package measure

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// MinShadowFires is how many shadow fires a rule needs before a rate is printed
// for it (docs/trellis-architecture.md §5: a rule keeps or changes its level only
// over its last 10 or more fires). Under it the report says so.
const MinShadowFires = 10

// ShadowHorizonDays is how long after a would-be block a downstream authority
// may still make it a catch, and a merge a pass: the document's 28 days of a
// merged change staying clean, as far as the log reaches.
const ShadowHorizonDays = 28

const shadowHorizon = ShadowHorizonDays * 24 * time.Hour

// ShadowRule is one rule's shadow fires: what the kernel would have decided
// beside what aphrollo did (internal/shadow). A disagreement is not a wrong
// block. Of the fires where trellis would block and aphrollo did not (Stricter,
// the would-be blocks), the log afterwards says which were wrong (an override on
// the lane within WrongBlockWindow), which were catches (a later commit gate
// refusal, red CI or escape on the lane), which passed (the lane merged with none
// of those) and which are still open. Softer is where aphrollo denies and trellis
// would only guide; SoftHeldOut those that were the holdout arm's by design.
type ShadowRule struct {
	Rule          string `json:"rule"`
	Fires         int    `json:"fires"`
	Agree         int    `json:"agree"`
	Stricter      int    `json:"trellis_stricter"`
	Softer        int    `json:"trellis_softer"`
	SoftHeldOut   int    `json:"trellis_softer_held_out"`
	Mismatch      int    `json:"verdict_mismatch"`
	NotComparable int    `json:"not_comparable"`
	HeldOut       int    `json:"held_out"`
	Catches       int    `json:"would_be_catches"`
	Wrong         int    `json:"would_be_wrong"`
	Passes        int    `json:"would_be_passes"`
	Open          int    `json:"would_be_open"`
}

// Rate is the share of the rule's fires where both sides agreed, "" while the
// rule has fewer than MinShadowFires.
func (r ShadowRule) Rate() string {
	if r.Fires < MinShadowFires {
		return ""
	}
	return fmt.Sprintf("%.0f%%", share(r.Agree, r.Fires)*100)
}

// Shadow is the shadow fires of a window, by rule.
type Shadow struct {
	Window string       `json:"window"`
	Fires  int          `json:"fires"`
	Notes  []string     `json:"notes"`
	Rules  []ShadowRule `json:"rules"`
}

// shadowNotes say what the numbers are not: they are read from facts the hooks
// recorded, and the kernel is not running as it will once it decides.
var shadowNotes = []string{
	"observed only where a hook acted: no fact exists where aphrollo did nothing, so agreement is overstated, and trellis acting alone is seen only for a waived primary-checkout write",
	"the kernel ran on its default config: aphrollo declares no rule pins or isolation setting to read, so the levels are the kernel's own",
	"run-verdict agreements are derived, approximately: run.result events minus the runs recorded here",
}

// ComputeShadow folds the shadow events of the window. What follows a fire is
// read from the whole log, not only the window.
func ComputeShadow(events []tdd.Event, now time.Time, o Options) Shadow {
	s := newScope(events, now, o)
	out := Shadow{Window: "whole log", Notes: shadowNotes}
	if o.Window > 0 {
		out.Window = "last " + windowText(o.Window)
	}
	rules := map[string]*ShadowRule{}
	var runResults, runRecorded int
	for i, e := range s.evs {
		if !s.in(e.at) {
			continue
		}
		if e.Kind == "run.result" {
			runResults++
		}
		if e.Kind != "shadow" {
			continue
		}
		if e.Detail["hook"] == "posttooluse-run" {
			runRecorded++
		}
		rule := e.Detail["rule"]
		if rule == "" {
			rule = "unknown"
		}
		r := rules[rule]
		if r == nil {
			r = &ShadowRule{Rule: rule}
			rules[rule] = r
		}
		out.Fires++
		r.Fires++
		held := e.Detail["held_out"] == "true"
		if held {
			r.HeldOut++
		}
		switch e.Detail["relation"] {
		case "agree":
			r.Agree++
		case "trellis-stricter":
			r.Stricter++
			switch followOf(s.evs, i) {
			case followWrong:
				r.Wrong++
			case followCatch:
				r.Catches++
			case followPass:
				r.Passes++
			default:
				r.Open++
			}
		case "trellis-softer":
			r.Softer++
			if held {
				r.SoftHeldOut++
			}
		case "verdict-mismatch":
			r.Mismatch++
		case "not-comparable":
			r.NotComparable++
		}
	}
	// A run both sides read alike writes no event: those agreements are the run
	// results the recorded ones do not account for.
	if plain := runResults - runRecorded; plain > 0 {
		r := rules["run-verdict"]
		if r == nil {
			r = &ShadowRule{Rule: "run-verdict"}
			rules["run-verdict"] = r
		}
		r.Fires += plain
		r.Agree += plain
		out.Fires += plain
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, *r)
	}
	sort.Slice(out.Rules, func(i, j int) bool { return out.Rules[i].Rule < out.Rules[j].Rule })
	return out
}

type follow int

const (
	followOpen follow = iota
	followWrong
	followCatch
	followPass
)

// followOf is what the log says of the would-be block at evs[i], read from the
// events of its own lane: an override of the same rule within WrongBlockWindow
// makes it wrong, whatever else follows; else a commit gate refusal, red CI or
// escape on the lane makes it a catch; else the lane's merge makes it a pass.
//
// The scan stops at the lane's first merge after the fire: a lane name used again
// afterwards is another lane. A fire on no lane, or in the primary checkout (its
// name is the trunk's, shared by every change made there, and the gate's events
// carry no root to tell them apart), is never joined and stays open. Events on no
// lane never join. An event that names a worktree other than the fire's is not
// the same lane's.
func followOf(evs []stamped, i int) follow {
	fire := evs[i]
	if fire.Lane == "" || fire.Detail["primary"] == "true" || isTrunkLane(fire.Lane) {
		return followOpen
	}
	wt := fire.Detail["wt"]
	caught := false
	for _, e := range evs[i+1:] {
		if e.at.Sub(fire.at) > shadowHorizon {
			break
		}
		if e.Lane != fire.Lane || (wt != "" && e.Root != "" && e.Root != wt) {
			continue
		}
		switch {
		case e.Kind == "override" && !isAllowedRerun(e) && e.at.Sub(fire.at) <= WrongBlockWindow && overrideAbout(e, fire):
			return followWrong
		case e.Kind == "commit_gate_result" && e.Verdict == "blocked",
			e.Kind == "ci" && e.Verdict == "red",
			e.Kind == "escape" && e.Verdict != "false-positive":
			caught = true
		case e.Kind == "merge" && e.Verdict == "ok":
			if caught {
				return followCatch
			}
			return followPass
		}
	}
	if caught {
		return followCatch
	}
	return followOpen
}

// isTrunkLane is a lane name that is a trunk's: the primary checkout's.
func isTrunkLane(lane string) bool {
	return lane == "main" || lane == "master" || lane == "@trunk"
}

// overrideWords are the words an override of a shadowed rule's wall carries in its
// name, beside the live rule's own name.
var overrideWords = map[string][]string{
	"primary-write": {"primary"},
	"discard-work":  {"discard"},
	"rerun-suite":   {"override-bash-"},
	"bypass-verb":   {"direct-pr", "pr-open"},
	"attribution":   {"undercover"},
}

// overrideAbout reports whether the override waives the rule the fire was about:
// the aphrollo rule's own name or the shadowed rule's wall, never an unrelated
// escape on the same lane.
func overrideAbout(o, fire stamped) bool {
	name := o.Detail["override"]
	if name == "" {
		name = o.Verdict
	}
	needles := slices.Clone(overrideWords[fire.Detail["rule"]])
	if live := fire.Detail["live_rule"]; live != "" {
		needles = append(needles, live, strings.TrimPrefix(live, "ratchet:"))
	}
	return slices.ContainsFunc(needles, func(n string) bool { return n != "" && strings.Contains(name, n) })
}

func windowText(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// Text is the shadow section as the plain lines `aphrollo stats --shadow` prints.
func (s Shadow) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	const cont = "                       " // under a row's first count
	p("%-22s%d (%s)", "shadow fires", s.Fires, s.Window)
	for _, n := range s.Notes {
		p("%-22s%s", "  note", n)
	}
	p("%-22s%s", "  wrong block", fmt.Sprintf("an override of the same rule within %.0f min of the fire on its lane", WrongBlockWindow.Minutes()))
	p("%-22s%s", "  catch / pass", fmt.Sprintf("a later commit gate refusal, red CI or escape / the lane's merge, within %d days", ShadowHorizonDays))
	for _, r := range s.Rules {
		p("%-22s %d fires  agree %d  would-be block %d  softer %d (%d held out)  mismatch %d  not comparable %d  held out %d",
			r.Rule, r.Fires, r.Agree, r.Stricter, r.Softer, r.SoftHeldOut, r.Mismatch, r.NotComparable, r.HeldOut)
		if r.Stricter > 0 {
			p("%swould-be blocks: catches %d  wrong %d  passes %d  open %d", cont, r.Catches, r.Wrong, r.Passes, r.Open)
		}
		if rate := r.Rate(); rate != "" {
			p("%sagreement %s of %d fires", cont, rate, r.Fires)
		} else {
			p("%sunder %d fires: no rate", cont, MinShadowFires)
		}
	}
	return b.String()
}
