// Package shadow records what the trellis kernel would have decided beside what
// a live aphrollo hook did (docs/trellis-architecture.md §12, F28 to F29). It is
// record-only: nothing here returns a decision to a hook, a record that cannot be
// written inside the budget is dropped, and no record carries a command's text.
//
// Only rules the kernel decides from facts the hook already holds are shadowed.
// Each is asked of kernel.Decide with an empty lane State and no units, so a rule
// that starts reading state would skew the data: the test of this package asks
// every shadowed rule under populated states and fails if the answer moves.
// Red→green needs a unit's state and the coverage of an edit, which no live hook
// has, so it is not shadowed and nothing is recorded for it.
//
// A record's relation says how the two decisions compare, ranked allow below
// guide or warn below block: agree, trellis-stricter (a would-be block), or
// trellis-softer (aphrollo denies where trellis would only guide). A
// disagreement is not a wrong block: those and the catches come only from what
// the log shows afterwards (measure.ComputeShadow).
package shadow

import (
	"strings"
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
	Guide    string
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
	t := trellisWord(d)
	rel := Agree
	switch a, b := rank(t), rank(string(f.Actual)); {
	case a > b:
		rel = TrellisStricter
	case a < b:
		rel = TrellisSofter
	}
	return Record{Hook: HookPre, Rule: f.Rule, LiveRule: f.LiveRule, Trellis: t, Actual: string(f.Actual),
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
	if d.Rule != "" {
		r.Rule = d.Rule
	}
	if verdictClass(f.Verdict) != verdictClass(actual) || (f.Verdict == kernel.VerdictNotTested && f.Cause != actualCause) {
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
	set("key", s.Key)
	set("guide", r.Guide)
	set("trellis_verdict", r.TrellisVerdict)
	set("aphrollo_verdict", r.ActualVerdict)
	set("cause", r.Cause)
	set("aphrollo_cause", r.ActualCause)
	if r.HeldOut {
		d["held_out"] = "true"
	}
	return core.Event{Kind: core.KindShadow, Root: s.Root, Actor: s.Actor, Lane: lane, Detail: d}
}

// bounded runs fn and waits for it no longer than Budget. fn runs on its own
// goroutine, so a hook that outruns the budget moves on; a panic in it is
// dropped, never raised into the hook.
func bounded(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		fn()
	}()
	t := time.NewTimer(Budget)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// RecordFacts judges each fact of a PreToolUse call and writes one shadow event
// for it. It never returns anything to the hook.
func RecordFacts(s Source, facts []Fact) {
	if len(facts) == 0 {
		return
	}
	bounded(func() {
		lane := core.LaneOf(s.Root)
		for _, f := range facts {
			appendEvent(Judge(f, lane).event(s, lane))
		}
	})
}

// RecordRun writes one shadow event for a finished run. The run's own facts are
// read by fact, inside the budget with the write: a hook that has to open a log
// to classify the run does not pay for it past the deadline. A fact that reports
// false is not recorded.
func RecordRun(s Source, fact func() (RunFact, bool)) {
	bounded(func() {
		f, ok := fact()
		if !ok {
			return
		}
		lane := core.LaneOf(s.Root)
		if r, ok := JudgeRun(f, lane); ok {
			appendEvent(r.event(s, lane))
		}
	})
}
