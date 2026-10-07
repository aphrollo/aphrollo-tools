package mutation

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// A partial map names the tests measured so far. What they miss is not a
// survivor: a test it does not name may kill the mutant. So a partial map
// never calls a line uncovered and never calls a mutant a survivor.

func covselectPartial(fn string, tests ...string) *testMap {
	m := mapFor(fn, tests...)
	m.Partial = true
	return m
}

func TestSelectTests_APartialMapSelectsWhatItNamesAndIsNeverExact(t *testing.T) {
	sel := selectTests(covselectPartial("Kind", "TestKind_A"), nil, nil, "gate.go", 5)
	if !slices.Equal(sel.Names, []string{"TestKind_A"}) || !sel.Partial || sel.Exact || sel.Whole {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestSelectTests_APartialMapWithNoTestOnALineIsNotUncovered(t *testing.T) {
	sel := selectTests(covselectPartial("Kind"), nil, nil, "gate.go", 5)
	if sel.Uncovered || !sel.Partial || len(sel.Names) != 0 {
		t.Fatalf("selection = %+v, want partial with nothing selected and not uncovered", sel)
	}
}

func TestSelectTests_ACompleteMapKeepsItsMeaning(t *testing.T) {
	if sel := selectTests(mapFor("Kind"), nil, nil, "gate.go", 5); !sel.Uncovered || sel.Partial {
		t.Fatalf("selection = %+v", sel)
	}
	if sel := selectTests(mapFor("Kind", "TestKind_A"), nil, nil, "gate.go", 5); !sel.Exact || sel.Partial {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestRunCommitMutants_APartialMapsSurvivorIsNotMeasuredAndNotRunAgainstThePackage(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	got := runCommitOnce(t, root, planFor(covselectPartial("Kind", "TestKind_A")), kindMutant, time.Minute)
	if got.NotMeasured == "" || got.GapKind != gapPartial {
		t.Fatalf("outcome %q, not measured %q kind %q, want a partial gap", got.Outcome.Status, got.NotMeasured, got.GapKind)
	}
	if s.count() != 1 || s.calls[0].Run != "^(TestKind_A)$" {
		t.Fatalf("go test calls = %+v, want one over the measured test, and no run of the rest", s.calls)
	}
	if !strings.Contains(got.NotMeasured, "partial") {
		t.Errorf("reason = %q, want it to say the map is partial", got.NotMeasured)
	}
}

func TestRunCommitMutants_APartialMapsKillIsStillACatch(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, failedGate
		}
		return 0, "ok\tgate\n"
	})
	got := runCommitOnce(t, root, planFor(covselectPartial("Kind", "TestKind_A")), kindMutant, time.Minute)
	if got.Outcome.Status != "caught" || got.NotMeasured != "" {
		t.Fatalf("outcome %q, not measured %q, want caught", got.Outcome.Status, got.NotMeasured)
	}
}

func TestRunCommitMutants_APartialMapWithNoTestForTheLineRunsNothing(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	got := runCommitOnce(t, root, planFor(covselectPartial("Kind")), kindMutant, time.Minute)
	if got.GapKind != gapPartial || s.count() != 0 {
		t.Fatalf("kind %q after %d go test runs, want a partial gap and none", got.GapKind, s.count())
	}
}

func TestCommitReport_PartialMutantsAreCountedUnderTheirOwnName(t *testing.T) {
	t.Parallel()
	_, measured, gaps := commitReport(MutantsConfig{}, []commitRun{{NotMeasured: "x", GapKind: gapPartial}})
	if measured != 0 || gaps != "1 partial" {
		t.Errorf("measured %d gaps %q", measured, gaps)
	}
}
