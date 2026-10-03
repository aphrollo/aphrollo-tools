package kernel

import "slices"

// Input is a column of the §3 TDD table: what an event means to a unit.
type Input string

const (
	InTestEdit    Input = "test-edit"    // a test T added or changed
	InTestRemoved Input = "test-removed" // T is gone
	InCodeEdit    Input = "code-edit"
	InRed         Input = "red" // a real red: red or red-missing-impl
	InGreen       Input = "green"
	InBogus       Input = "red-bogus"
	InPending     Input = "pending"    // not tested: deferred
	InNotTested   Input = "not-tested" // not tested: timeout, skipped, infra, anything unknown
	InCIEscape    Input = "ci-escape"  // a CI test red on a head whose note says gated green
	InCIGreen     Input = "ci-green"
	InTrunkEscape Input = "trunk-escape"
	InMergeCloses Input = "merge-closes" // a merge whose closes-by names escapes
)

// classify reads an event as a column, "" for an event the machine ignores.
func classify(e Event) Input {
	switch e.Kind {
	case KindEdit:
		switch {
		case e.File == ClassTest && e.Removed:
			return InTestRemoved
		case e.File == ClassTest:
			return InTestEdit
		case e.File == ClassCode:
			return InCodeEdit
		}
	case KindRunResult:
		switch e.Verdict {
		case VerdictGreen:
			return InGreen
		case VerdictRed, VerdictRedMissingImpl:
			return InRed
		case VerdictRedBogus:
			return InBogus
		}
		if e.Cause == CauseDeferred {
			return InPending
		}
		return InNotTested
	case KindCIVerdict:
		switch {
		case e.Conclusion == CIRed && e.Gated && e.Failure == FailureTest:
			return InCIEscape
		case e.Conclusion == CIGreen:
			return InCIGreen
		}
	case KindEscape:
		if e.Stage == StageTrunk {
			return InTrunkEscape
		}
	case KindLaneMerged:
		if len(e.Closes) > 0 {
			return InMergeCloses
		}
	}
	return ""
}

// UnitGuard narrows a row beyond its From and In. It sees the unit before the
// event and the event.
type UnitGuard func(old Unit, e Event) bool

// UnitRow is one transition: in any From phase, an input whose guard holds
// moves the unit to To ("" keeps the phase), runs Act on the unit with the
// event's facts folded in, and asks for Guide. The first matching row wins; an
// input with no matching row is the table's "Stay". Rule names its source.
type UnitRow struct {
	Rule  string
	From  []Phase
	In    Input
	When  UnitGuard
	To    Phase
	Act   func(next, old Unit, e Event) Unit
	Guide string
}

var (
	livePhases      = []Phase{PhaseClosed, PhasePending, PhaseOpen, PhaseHeld}
	closedOrPending = []Phase{PhaseClosed, PhasePending}
	openOrPending   = []Phase{PhaseOpen, PhasePending}
	openOrHeld      = []Phase{PhaseOpen, PhaseHeld}
)

// unitTable is the TDD machine: data, read only. It is the §3 table read
// column by column; the rows after it are §6's "the cause is named".
var unitTable = []UnitRow{
	{Rule: "§3 TDD machine: closed or held + test added or changed → pending(T)",
		From: []Phase{PhaseClosed, PhaseHeld}, In: InTestEdit, To: PhasePending, Act: startTest},
	{Rule: "§3 TDD machine: closed, pending or held + real red of T → open(T); a held unit's red releases the hold",
		From: []Phase{PhaseClosed, PhasePending, PhaseHeld}, In: InRed, When: reproduces, To: PhaseOpen, Act: openFromRed},
	{Rule: "§3 TDD machine: open + real red → stay open; the red moves to the newest tree",
		From: []Phase{PhaseOpen}, In: InRed, When: reproduces, Act: moveRed},
	{Rule: "§3 TDD machine, tested code (S12): closed + code edit that is not tested code → one guidance line per unit per lane (enforce: deny)",
		From: []Phase{PhaseClosed}, In: InCodeEdit, When: untestedCode, Guide: GuideUntestedCode},
	{Rule: "§3 TDD machine (C9): held + code edit → guidance at every level",
		From: []Phase{PhaseHeld}, In: InCodeEdit, Guide: GuideHeld},
	{Rule: "§3 TDD machine (C2): pending + T green → closed: T passed at once, not a red",
		From: []Phase{PhasePending}, In: InGreen, To: PhaseClosed, Act: closeUnit, Guide: GuidePassedAtOnce},
	{Rule: "§3 TDD machine, §6 caching: open + green at the red's own tree → a flaky disagreement, stay open",
		From: []Phase{PhaseOpen}, In: InGreen, When: atRedTree, Guide: GuideFlaky},
	{Rule: "§3 TDD machine: open + T green after a code change → closed; the red→green pair is recorded",
		From: []Phase{PhaseOpen}, In: InGreen, When: pairs, To: PhaseClosed, Act: closeWithPair},
	{Rule: "§3 TDD machine: open + T then passes with no code change → closed, no pair",
		From: []Phase{PhaseOpen}, In: InGreen, To: PhaseClosed, Act: closeUnit},
	{Rule: "§3 TDD machine: open or pending + T removed → closed",
		From: openOrPending, In: InTestRemoved, When: namesTest, To: PhaseClosed, Act: closeUnit},
	{Rule: "§8 escapes: closed or pending + CI test red on a gated head → open(T); the red is the reproduction",
		From: closedOrPending, In: InCIEscape, To: PhaseOpen, Act: openFromCI},
	{Rule: "§3 TDD machine, §8 escapes: closed or pending + trunk product escape → held",
		From: closedOrPending, In: InTrunkEscape, To: PhaseHeld, Act: holdUnit},
	{Rule: "§8 escapes: open or held + CI green of the named test on a later head → closed",
		From: openOrHeld, In: InCIGreen, When: laterGreen, To: PhaseClosed, Act: closeUnit},
	{Rule: "§8 escapes: held + merge of a lane whose closes-by names the escape → closed",
		From: []Phase{PhaseHeld}, In: InMergeCloses, When: closesHold, To: PhaseClosed, Act: closeUnit},
	{Rule: "§3 TDD machine: a bogus red is not a red; stay, the cause is named",
		From: livePhases, In: InBogus, Guide: GuideRedBogus},
	{Rule: "§6 tiers: a deferred run is pending, not untested; stay",
		From: livePhases, In: InPending, Guide: GuidePending},
	{Rule: "§3 TDD machine, §6 tiers: a run that did not test moves no state; stay, the cause is named",
		From: livePhases, In: InNotTested, Guide: GuideNotTested},
}

// reproduces holds when a red of test f is a red of the unit's T. With no T
// named on either side the red is taken as T's: reading it as unrelated could
// only leave a unit closed that should be open.
func reproduces(old Unit, e Event) bool { return old.Test == "" || e.Test == "" || e.Test == old.Test }

func namesTest(old Unit, e Event) bool { return e.Test == "" || e.Test == old.Test }

// untestedCode is the negation of §3's "tested code" with its facts from the
// adapter: covered by a passing test of the unit, and adding no exported
// symbol and no new function.
func untestedCode(_ Unit, e Event) bool { return !e.Covered || e.AddsSymbol }

func atRedTree(old Unit, e Event) bool { return old.RedTree != "" && e.Tree == old.RedTree }

// pairs holds for a green that follows a code change on a later tree than the
// red, for a test that is new or changed.
func pairs(old Unit, e Event) bool {
	return old.Earns && old.Code && old.RedTree != "" && e.Tree != old.RedTree
}

// laterGreen holds for a CI green of the named test on a head other than the
// one the escape was about.
func laterGreen(old Unit, e Event) bool {
	return e.Test != "" && e.Test == old.Test && (old.Head == "" || e.Head != old.Head)
}

func closesHold(old Unit, e Event) bool { return old.Hold != "" && slices.Contains(e.Closes, old.Hold) }

// cleared is the unit with no T, hold or red outstanding.
func cleared(n Unit) Unit {
	n.Test, n.Earns, n.RedTree, n.Code, n.Hold, n.Head = "", false, "", false, "", ""
	return n
}

func startTest(n, _ Unit, e Event) Unit {
	n = cleared(n)
	n.Test, n.Earns = e.Test, true
	return n
}

// openFromRed opens the unit on a local red. A pending T keeps earning the
// pair; a test this lane's edits broke, or a held unit's, does not.
func openFromRed(n, old Unit, e Event) Unit {
	n = cleared(n)
	n.Test, n.Earns, n.RedTree = first(old.Test, e.Test), old.Phase == PhasePending && old.Earns, e.Tree
	return n
}

// moveRed keeps the red nearest the green: a red at a new tree starts a new
// count of code edits.
func moveRed(n, old Unit, e Event) Unit {
	n.RedTree, n.Code = e.Tree, n.Code && e.Tree == old.RedTree
	return n
}

// openFromCI opens the unit on CI's test red. The red's tree is CI's, not the
// local worktree's, so none is recorded and a local green at any tree closes it.
func openFromCI(n, old Unit, e Event) Unit {
	n = cleared(n)
	n.Test, n.Head = first(e.Test, old.Test), e.Head
	return n
}

func holdUnit(n, _ Unit, e Event) Unit {
	n = cleared(n)
	n.Test, n.Hold, n.Head = e.Test, e.EscapeID, e.Head
	return n
}

func closeUnit(n, _ Unit, _ Event) Unit { return cleared(n) }

func closeWithPair(n, old Unit, e Event) Unit {
	n = cleared(n)
	n.Pair = Pair{Test: old.Test, Red: old.RedTree, Green: e.Tree}
	return n
}
