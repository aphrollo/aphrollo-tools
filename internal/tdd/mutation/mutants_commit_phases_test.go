package mutation

import (
	"context"
	"io"
	"slices"
	"testing"
	"time"
)

// The commit-time run settles every mutant against its selected tests before
// any mutant is run against the whole package, keeps each worker's copy of the
// lane at a path every run reuses, runs only the tests a selection left when
// it confirms a survivor, and asks once whether a failing test also fails
// without the mutant.

// The go build cache keys a compile on the directory it ran in: a worker's
// copy is at the same path every run, and the first worker keeps the proofs'
// slot while each other worker has a slot of its own.
func TestWorkerSlot_NamesEachWorkersSlot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		worker int
		want   string
	}{
		{-1, proveSandboxSlot},
		{0, proveSandboxSlot},
		{1, "run-w1"},
		{2, "run-w2"},
	} {
		if got := workerSlot(tc.worker); got != tc.want {
			t.Errorf("workerSlot(%d) = %q, want %q", tc.worker, got, tc.want)
		}
	}
}

func TestNewWorkerSandbox_AWorkersCopyIsAtOnePathEveryRunAndApartFromTheOthers(t *testing.T) {
	lane := laneWithWork(t)
	rootOf := func(worker int) string {
		box, err := newWorkerSandbox(lane, lane, worker)
		if err != nil {
			t.Fatalf("worker %d: %v", worker, err)
		}
		defer box.remove()
		return box.root
	}

	first, again := rootOf(1), rootOf(1)
	other := rootOf(0)

	if first != again {
		t.Errorf("worker 1 ran in %q and then %q, want one path", first, again)
	}
	if first == other {
		t.Errorf("workers 0 and 1 share the copy %q", first)
	}
	assertNoSandboxLeft(t, lane)
}

// A mutant with no selection waits for the whole-package run, and the mutants
// with one are all settled first, whatever order they came in.
func TestRunCommitMutants_SelectedRunsComeBeforeAnyWholePackageRun(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	plans := planFor(mapFor("Kind", "TestKind_A"))

	runs := runCommitMutants(context.Background(), root, MutantsConfig{}, plans, []commitMutant{labelMutant, kindMutant}, 1, time.Minute, io.Discard)

	if len(runs) != 2 || runs[0].Outcome.Status != "missed" || runs[1].Outcome.Status != "missed" {
		t.Fatalf("runs = %+v, want both judged", runs)
	}
	var order []string
	for _, c := range s.calls {
		order = append(order, c.Run)
	}
	if want := []string{"^(TestKind_A)$", ""}; !slices.Equal(order, want) {
		t.Errorf("-run of each go test = %q, want the selection first and then the rest unselected", order)
	}
}

// A selection that passed is not run again: the rest of the package is what is
// left, which together with the selection is all of it.
func TestRunCommitMutants_AConfirmationSkipsTheTestsTheSelectionRan(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })

	got := runCommitOnce(t, root, touchedPlan("TestKind_A", "TestKind_B"), kindMutant, time.Minute)

	if got.Outcome.Status != "missed" || !got.WholePackage || s.count() != 2 {
		t.Fatalf("outcome %q whole %v after %d runs, want missed by the whole package in two runs", got.Outcome.Status, got.WholePackage, s.count())
	}
	if first := s.calls[0]; first.Run != "^(TestKind_A|TestKind_B)$" || slices.Contains(first.Args, "-skip") {
		t.Errorf("first run = %+v, want the selection alone", first)
	}
	second := s.calls[1]
	if skip := valueAfter(second.Args, "-skip"); skip != "^(TestKind_A|TestKind_B)$" || second.Run != "" || !second.Overlay {
		t.Errorf("second run = %+v with -skip %q, want the mutant run with everything but the selection skipped", second, skip)
	}
}

// Two mutants that fail the same test share one check of that test without
// the mutant; a test that fails under one mutant only is checked on its own.
func TestRunCommitMutants_AFailingTestIsCheckedWithoutTheMutantOnce(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, failedGate
		}
		return 0, "ok\tgate\n"
	})
	plans := planFor(mapFor("Kind", "TestKind_A"))

	runs := runCommitMutants(context.Background(), root, MutantsConfig{}, plans, []commitMutant{kindMutant, kindMutant, kindMutant}, 1, time.Minute, io.Discard)

	for i, r := range runs {
		if r.Outcome.Status != "caught" {
			t.Errorf("mutant %d: outcome %q (%s), want caught", i, r.Outcome.Status, r.NotMeasured)
		}
	}
	var plain int
	for _, c := range s.calls {
		if !c.Overlay {
			plain++
		}
	}
	if plain != 1 || s.count() != 4 {
		t.Errorf("%d runs without the mutant among %d, want 1 of 4 (three mutants, one shared check)", plain, s.count())
	}
}

func TestKillChecks_RemembersOnlyWhatWasAnswered(t *testing.T) {
	t.Parallel()
	asked := 0
	ask := func(v resolveVerdict) func() (resolveVerdict, string) {
		return func() (resolveVerdict, string) { asked++; return v, "why" }
	}
	k := &killChecks{}

	for range 2 {
		if v, d := k.check([]string{"a"}, []string{"-run", "x"}, ask(resolveSurvived)); v != resolveSurvived || d != "why" {
			t.Errorf("survived answer = %v %q", v, d)
		}
	}
	if asked != 1 {
		t.Errorf("asked %d times for one key, want 1", asked)
	}
	k.check([]string{"a"}, []string{"-run", "y"}, ask(resolveSurvived))
	k.check([]string{"b"}, []string{"-run", "x"}, ask(resolveSurvived))
	if asked != 3 {
		t.Errorf("asked %d times after a new extra and a new package, want 3", asked)
	}
	for range 2 {
		k.check([]string{"c"}, nil, ask(resolveKilled))
	}
	if asked != 4 {
		t.Errorf("a killed answer asked %d times in all, want it remembered (4)", asked)
	}
	for range 2 {
		k.check([]string{"d"}, nil, ask(resolveCutOff))
	}
	if asked != 6 {
		t.Errorf("a cut-off answer asked %d times in all, want it asked each time (6)", asked)
	}
}

func TestKillChecks_KeysDoNotRunTogether(t *testing.T) {
	t.Parallel()
	asked := 0
	ask := func() (resolveVerdict, string) { asked++; return resolveSurvived, "" }
	k := &killChecks{}

	k.check([]string{"a", "b"}, nil, ask)
	k.check([]string{"a"}, []string{"b"}, ask)

	if asked != 2 {
		t.Errorf("asked %d times, want a package split from a flag", asked)
	}
}

func TestKillChecks_NilRemembersNothing(t *testing.T) {
	t.Parallel()
	asked := 0
	var k *killChecks
	for range 2 {
		k.check([]string{"a"}, nil, func() (resolveVerdict, string) { asked++; return resolveSurvived, "" })
	}
	if asked != 2 {
		t.Errorf("asked %d times, want each time", asked)
	}
}

// A mutant's time is the time of its runs, the selected one and the one
// against the rest of the package together. The clock steps one second per
// reading, two readings to a run.
func TestRunCommitMutants_TookCountsBothRunsOfAMutant(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	var readings int
	prev := commitNowFn
	commitNowFn = func() time.Time {
		readings++
		return time.Unix(int64(readings), 0)
	}
	t.Cleanup(func() { commitNowFn = prev })

	got := runCommitOnce(t, root, touchedPlan("TestKind_A"), kindMutant, time.Minute)

	if got.Took != 2*time.Second {
		t.Errorf("Took = %s, want 2s for two runs of 1s each", got.Took)
	}
}
