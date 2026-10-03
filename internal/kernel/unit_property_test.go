package kernel

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

var allPhases = []Phase{PhaseClosed, PhasePending, PhaseOpen, PhaseHeld}

var (
	unitTrees = []string{"", "t1", "t2", "t3"}
	unitTests = []string{"", "TestA", "TestB"}
)

func genUnit() *rapid.Generator[Unit] {
	return rapid.Custom(func(t *rapid.T) Unit {
		return Unit{
			Phase:          rapid.SampledFrom(append([]Phase{"", "garbled"}, allPhases...)).Draw(t, "phase"),
			Test:           rapid.SampledFrom(unitTests).Draw(t, "test"),
			Earns:          rapid.Bool().Draw(t, "earns"),
			Tree:           rapid.SampledFrom(unitTrees).Draw(t, "tree"),
			Requested:      rapid.SampledFrom(unitTrees).Draw(t, "requested"),
			RedTree:        rapid.SampledFrom(unitTrees).Draw(t, "redTree"),
			Code:           rapid.Bool().Draw(t, "code"),
			Head:           rapid.SampledFrom([]string{"", "h1", "h2"}).Draw(t, "head"),
			Hold:           rapid.SampledFrom([]string{"", "esc-1", "esc-2"}).Draw(t, "hold"),
			Unproven:       rapid.Bool().Draw(t, "unproven"),
			LastReal:       rapid.SampledFrom([]Verdict{"", VerdictGreen, VerdictRed}).Draw(t, "lastReal"),
			LastRealTree:   rapid.SampledFrom(unitTrees).Draw(t, "lastRealTree"),
			LastJob:        rapid.SampledFrom([]string{"", "j1"}).Draw(t, "lastJob"),
			GuidedUntested: rapid.Bool().Draw(t, "guidedUntested"),
			GuidedHeld:     rapid.Bool().Draw(t, "guidedHeld"),
		}
	})
}

var unitKinds = []Kind{
	KindEdit, KindRunRequested, KindRunResult, KindCIVerdict, KindCommitGated,
	KindEscape, KindLaneMerged, KindLaneOpened, "garbage",
}

func genUnitEvent() *rapid.Generator[Event] {
	return rapid.Custom(func(t *rapid.T) Event {
		return Event{
			Kind:       rapid.SampledFrom(unitKinds).Draw(t, "kind"),
			Lane:       "fix",
			Actor:      "s/a",
			At:         t0,
			Unit:       "pkg/a",
			Tree:       rapid.SampledFrom(unitTrees).Draw(t, "tree"),
			File:       rapid.SampledFrom([]FileClass{"", ClassCode, ClassTest, ClassOther}).Draw(t, "file"),
			Removed:    rapid.Bool().Draw(t, "removed"),
			Test:       rapid.SampledFrom(unitTests).Draw(t, "test"),
			Job:        rapid.SampledFrom([]string{"", "j1", "j2"}).Draw(t, "job"),
			Verdict:    rapid.SampledFrom([]Verdict{"", VerdictGreen, VerdictRed, VerdictRedMissingImpl, VerdictRedBogus, VerdictNotTested, "odd"}).Draw(t, "verdict"),
			Cause:      rapid.SampledFrom([]string{"", CauseTimeout, CauseSkipped, CauseQueuedSkipped, CauseDeferred, CauseInfra}).Draw(t, "cause"),
			Covered:    rapid.Bool().Draw(t, "covered"),
			AddsSymbol: rapid.Bool().Draw(t, "addsSymbol"),
			Mode:       rapid.SampledFrom([]TDDMode{"", ModeWarn, ModeEnforce, ModeOff, "odd"}).Draw(t, "mode"),
			Gated:      rapid.Bool().Draw(t, "gated"),
			Failure:    rapid.SampledFrom([]string{"", FailureTest, FailureMutation, FailureFlaky}).Draw(t, "failure"),
			Stage:      rapid.SampledFrom([]string{"", StageTrunk, "odd"}).Draw(t, "stage"),
			EscapeID:   rapid.SampledFrom([]string{"", "esc-1", "esc-2"}).Draw(t, "escapeID"),
			Closes:     rapid.SliceOfN(rapid.SampledFrom([]string{"esc-1", "esc-2"}), 0, 2).Draw(t, "closes"),
			Conclusion: rapid.SampledFrom([]Conclusion{"", CIGreen, CIRed, CIStarted}).Draw(t, "conclusion"),
			Head:       rapid.SampledFrom([]string{"", "h1", "h2"}).Draw(t, "headOf"),
		}
	})
}

func TestStepUnit_isTotalAndDeterministicAndNamesItsUnit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u, e := genUnit().Draw(t, "unit"), genUnitEvent().Draw(t, "event")
		a, fxA := StepUnit(u, e)
		b, fxB := StepUnit(u, e)
		if a != b || !reflect.DeepEqual(fxA, fxB) {
			t.Fatalf("two calls disagree: %+v %v vs %+v %v", a, fxA, b, fxB)
		}
		for _, f := range fxA {
			if f.Unit != e.Unit || f.Lane != e.Lane {
				t.Fatalf("effect %+v names a unit or lane other than the event's", f)
			}
		}
	})
}

func TestStepUnit_aResultForAnotherTreeThanTheUnitsNeverChangesState(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u, e := genUnit().Draw(t, "unit"), genUnitEvent().Draw(t, "event")
		if u.Tree == "" {
			u.Tree = "t1"
		}
		e.Kind = KindRunResult
		e.Tree = rapid.SampledFrom(slices.DeleteFunc(slices.Clone(unitTrees), func(s string) bool { return s == u.Tree })).Draw(t, "otherTree")
		got, fx := StepUnit(u, e)
		if got != u {
			t.Fatalf("a result for tree %q moved a unit at %q: %+v -> %+v", e.Tree, u.Tree, u, got)
		}
		for _, f := range fx {
			if f.Kind != EffectGuide || f.Detail != GuideStale {
				t.Fatalf("a stale result emitted %+v, want only a stale-labelled guide", f)
			}
		}
	})
}

func TestStepUnit_aNotTestedResultNeverMovesPhaseLastRealPairOrUnproven(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u, e := genUnit().Draw(t, "unit"), genUnitEvent().Draw(t, "event")
		e.Kind = KindRunResult
		e.Verdict = rapid.SampledFrom([]Verdict{"", VerdictNotTested, VerdictRedBogus, "odd"}).Draw(t, "notTested")
		got, _ := StepUnit(u, e)
		if got.Phase != u.Phase {
			t.Fatalf("%q result moved the phase %q -> %q", e.Verdict, u.Phase, got.Phase)
		}
		if got.LastReal != u.LastReal || got.LastRealTree != u.LastRealTree || got.Pair != u.Pair || got.Unproven != u.Unproven {
			t.Fatalf("%q result changed what only a real run may: %+v -> %+v", e.Verdict, u, got)
		}
	})
}

func TestStepUnit_aUnitLeavesOpenOrPendingForClosedOnlyOnAGreen(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u := Unit{}
		for i, e := range rapid.SliceOfN(genUnitEvent(), 1, 40).Draw(t, "events") {
			if e.Kind != KindRunResult && e.Kind != KindEdit && e.Kind != KindRunRequested && e.Kind != KindCommitGated {
				continue // escapes, CI and merges have their own doors; this one is the run door
			}
			e.Removed = false
			next, _ := StepUnit(u, e)
			if (u.Phase == PhaseOpen || u.Phase == PhasePending) && next.Phase == PhaseClosed && (e.Kind != KindRunResult || e.Verdict != VerdictGreen) {
				t.Fatalf("event %d (%s, verdict %q) took %q to closed", i, e.Kind, e.Verdict, u.Phase)
			}
			u = next
		}
	})
}

func TestStepUnit_aPairNeedsTheRedOnAnEarlierTreeThanTheGreen(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u := Unit{}
		redTrees := map[string]bool{}
		for i, e := range rapid.SliceOfN(genUnitEvent(), 1, 50).Draw(t, "events") {
			next, _ := StepUnit(u, e)
			if next.Pair != u.Pair {
				p := next.Pair
				if e.Kind != KindRunResult || e.Verdict != VerdictGreen || e.Tree != p.Green {
					t.Fatalf("event %d (%s %q) recorded pair %+v: only a green result may", i, e.Kind, e.Verdict, p)
				}
				if p.Red == "" || p.Red == p.Green || !redTrees[p.Red] {
					t.Fatalf("event %d recorded pair %+v: the red must be a real red on a tree seen earlier and other than the green's", i, p)
				}
			}
			if e.Kind == KindRunResult && (e.Verdict == VerdictRed || e.Verdict == VerdictRedMissingImpl) {
				redTrees[e.Tree] = true
			}
			u = next
		}
	})
}

func TestStepUnit_offFreezesAnyUnitAgainstAnyEvent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u, e := genUnit().Draw(t, "unit"), genUnitEvent().Draw(t, "event")
		e.Mode = ModeOff
		got, fx := StepUnit(u, e)
		if got != u || len(fx) != 0 {
			t.Fatalf("off: %+v %v, want the unit untouched and no effects", got, fx)
		}
	})
}

func TestStepUnit_aDuplicateEventLeavesTheStateWhereOnceLeftIt(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u, e := genUnit().Draw(t, "unit"), genUnitEvent().Draw(t, "event")
		once, _ := StepUnit(u, e)
		twice, _ := StepUnit(once, e)
		if once != twice {
			t.Fatalf("event twice differs from once:\nonce  %+v\ntwice %+v", once, twice)
		}
	})
}

func TestStepUnit_phaseStaysInTheKnownSetFromAnyReachableStart(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		u := Unit{}
		for i, e := range rapid.SliceOfN(genUnitEvent(), 1, 40).Draw(t, "events") {
			u, _ = StepUnit(u, e)
			if u.Phase != "" && !slices.Contains(allPhases, u.Phase) {
				t.Fatalf("event %d (%s) left phase %q outside the table", i, e.Kind, u.Phase)
			}
		}
	})
}

func TestStepUnits_isDeterministicAndNeverWritesIntoItsInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := Units{"pkg/a": genUnit().Draw(t, "a"), "pkg/b": genUnit().Draw(t, "b")}
		e := genUnitEvent().Draw(t, "event")
		e.Unit = rapid.SampledFrom([]string{"", "pkg/a", "pkg/b", "pkg/c"}).Draw(t, "unit")
		before := maps.Clone(in)
		a, fxA := StepUnits(in, e)
		b, fxB := StepUnits(in, e)
		if !reflect.DeepEqual(in, before) {
			t.Fatalf("StepUnits wrote into its input: %+v -> %+v", before, in)
		}
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(fxA, fxB) {
			t.Fatalf("two calls disagree: %+v %v vs %+v %v", a, fxA, b, fxB)
		}
	})
}
