package kernel

import (
	"maps"
	"slices"
)

// Phase is where one unit's TDD machine stands (§3 "TDD machine"). The zero
// value reads as closed until a row moves it.
type Phase string

const (
	PhaseClosed  Phase = "closed"
	PhasePending Phase = "pending" // a test T was added or changed and its verdict is on its way
	PhaseOpen    Phase = "open"    // a real red of T is outstanding: code edits are allowed
	PhaseHeld    Phase = "held"    // a trunk escape holds the unit: guidance only
)

// Guide codes name the one line a guide effect asks render for.
const (
	GuideUntestedCode = "untested-code" // closed + code edit that is not tested code
	GuideHeld         = "held"
	GuidePassedAtOnce = "passed-at-once" // "T passed at once; not a red"
	GuideFlaky        = "flaky"          // green and red at one unchanged tree
	GuideRedBogus     = "red-bogus"
	GuidePending      = "pending"    // deferred: the verdict is on its way
	GuideNotTested    = "not-tested" // the effect's Cause says why
	GuideStale        = "stale"      // a verdict for a tree that has moved on
)

// Pair is the recorded red→green pair the commit proof reads (§3): the test,
// the tree it was red on and the later tree it went green on.
type Pair struct{ Test, Red, Green string }

// Unit is one unit's record: the lane record's tdd:{unit: {state, test,
// last_real}} (§3) with the facts the transitions read. It holds no map or
// slice, so a copy is a clone.
type Unit struct {
	Phase Phase
	Test  string // T: the pending or open test, or the held escape's reproducing test
	// Earns is true while T is a new or changed test; only such a T earns the
	// red→green pair (§3).
	Earns bool

	Tree      string // the newest worktree key the unit has seen; a verdict for another tree is stale
	Requested string // the tree of the run on its way, "" when none
	RedTree   string // the tree of T's newest real red while open
	Code      bool   // a code edit since that red
	Head      string // the CI head an escape was about
	Hold      string // the escape id while held
	Unproven  bool   // a write nobody parsed, until the next real run

	LastReal     Verdict // green or red: the last real verdict, which stands through not-tested
	LastRealTree string
	LastJob      string // the newest result job applied, so a retried transaction is idempotent
	Pair         Pair

	GuidedUntested, GuidedHeld bool // the one guidance line per unit per lane was given
}

// Units is a lane's TDD record, unit id to machine state.
type Units map[string]Unit

// StepUnit is the TDD machine of one unit: pure and total. A verdict is a fact
// about a tree (§1): a run result for a tree other than the unit's newest is
// stale, and moves nothing, but is still delivered, labelled (§6 "Caching",
// #813). A result of a run that did not test never moves a state (§3 "Writes
// nobody parsed"). Under tdd = off the machine is frozen (§3).
func StepUnit(u Unit, e Event) (Unit, []Effect) {
	next, fx, _ := advanceUnit(u, e)
	return next, fx
}

// advanceUnit is StepUnit plus the index of the table row that decided, -1
// when none did.
func advanceUnit(u Unit, e Event) (Unit, []Effect, int) {
	if e.Mode == ModeOff {
		return u, nil, -1
	}
	if e.Kind == KindRunResult {
		if e.Job != "" && e.Job == u.LastJob {
			return u, nil, -1
		}
		if u.Tree != "" && e.Tree != u.Tree {
			f, _ := guideEffect(&u, GuideStale, e)
			return u, []Effect{f}, -1
		}
	}
	next := foldUnit(u, e)
	var fx []Effect
	if e.Kind == KindEdit && e.File != ClassOther {
		f := unitEffect(EffectRequestRun, e, "")
		f.Tree = e.Tree
		fx = append(fx, f)
	}
	i := matchUnit(u, e)
	if i < 0 {
		return next, fx, -1
	}
	row := unitTable[i]
	if row.Act != nil {
		next = row.Act(next, u, e)
	}
	if row.To != "" {
		next.Phase = row.To
	}
	if row.Guide != "" {
		if f, ok := guideEffect(&next, row.Guide, e); ok {
			fx = append(fx, f)
		}
	}
	return next, fx, i
}

// matchUnit returns the first row for the unit's phase and the event's input
// whose guard holds, or -1.
func matchUnit(u Unit, e Event) int {
	in, phase := classify(e), u.Phase
	if phase == "" {
		phase = PhaseClosed
	}
	for i, r := range unitTable {
		if r.In == in && slices.Contains(r.From, phase) && (r.When == nil || r.When(u, e)) {
			return i
		}
	}
	return -1
}

// foldUnit folds the event's facts into the unit. Every fold is a set or a
// latest-wins, so folding the same event again changes nothing.
func foldUnit(u Unit, e Event) Unit {
	switch e.Kind {
	case KindEdit:
		u.Tree = first(e.Tree, u.Tree)
		u.Unproven = u.Unproven || e.File == ""
		u.Code = u.Code || (e.File == ClassCode && u.Phase == PhaseOpen)
	case KindRunRequested:
		u.Tree = first(u.Tree, e.Tree)
		if e.Tree == u.Tree {
			u.Requested = e.Tree
		}
	case KindRunResult:
		u.LastJob = e.Job
		u.Tree = first(u.Tree, e.Tree)
		if e.Tree == u.Requested && e.Cause != CauseDeferred {
			u.Requested = ""
		}
		if v := realVerdict(e.Verdict); v != "" {
			u.Unproven, u.LastReal, u.LastRealTree = false, v, e.Tree
		}
	case KindCommitGated:
		if u.LastReal == "" {
			u.LastReal, u.LastRealTree = VerdictGreen, e.Tree
		}
	}
	return u
}

// realVerdict is green or red for a run that tested, "" for one that did not.
func realVerdict(v Verdict) Verdict {
	switch v {
	case VerdictGreen:
		return VerdictGreen
	case VerdictRed, VerdictRedMissingImpl:
		return VerdictRed
	}
	return ""
}

func unitEffect(k EffectKind, e Event, detail string) Effect {
	return Effect{Kind: k, Lane: e.Lane, Unit: e.Unit, Detail: detail}
}

// guideEffect builds a row's guide. Guidance comes once per unit per lane
// (§5 "Tokens"), recorded on the unit; under enforce the untested-code line
// comes every time and is marked as a deny the rule table may make.
func guideEffect(n *Unit, code string, e Event) (Effect, bool) {
	f := unitEffect(EffectGuide, e, code)
	f.Test, f.Cause, f.Tree = n.Test, e.Cause, e.Tree
	switch code {
	case GuideUntestedCode:
		enforce := e.Mode == ModeEnforce
		if n.GuidedUntested && !enforce {
			return f, false
		}
		n.GuidedUntested, f.WouldDeny = true, enforce
	case GuideHeld:
		if n.GuidedHeld {
			return f, false
		}
		n.GuidedHeld = true
	}
	return f, true
}

// StepUnits routes an event to the unit it names and returns a new map; the
// argument is never written into. An event naming no unit moves none, except a
// gated commit, which seeds every unit's last real verdict (§3 "Lane record").
func StepUnits(us Units, e Event) (Units, []Effect) {
	out := maps.Clone(us)
	if out == nil {
		out = Units{}
	}
	names := []string{e.Unit}
	if e.Unit == "" {
		if e.Kind != KindCommitGated {
			return out, nil
		}
		names = slices.Sorted(maps.Keys(us))
	}
	var fx []Effect
	for _, name := range names {
		e.Unit = name
		u, f := StepUnit(us[name], e)
		out[name] = u
		fx = append(fx, f...)
	}
	return out, fx
}
