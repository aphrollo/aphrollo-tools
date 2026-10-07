package mutation

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// The commit stage measures coverage itself: one run of the package's tests
// for the line-to-tests map, then each mutant against the tests that execute
// its line only, and a mutant on a line none executes is named, not run.

// A mutant on a line the coverage run shows no test executes could not be
// killed by any, so it is named not covered and no `go test` is started for it.
func TestRunCommitMutants_ALineNoTestExecutesIsNamedNotCoveredAndNotRun(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	m := mapFor("Kind") // Kind's block is listed with no test that ran it

	got := runCommitOnce(t, root, planFor(m), kindMutant, time.Minute)

	if got.NotMeasured == "" || got.GapKind != gapNotCovered {
		t.Fatalf("not measured %q kind %q, want a not-covered gap", got.NotMeasured, got.GapKind)
	}
	if !strings.Contains(got.NotMeasured, "no test of gate executes this line") {
		t.Errorf("reason = %q, want it to say no test executes the line", got.NotMeasured)
	}
	if s.count() != 0 {
		t.Errorf("go test ran %d times, want none for a line no test executes", s.count())
	}
}

// A mutant its covering tests miss is a survivor at once: a test that does
// not execute the line cannot kill it, so no run of the rest of the package
// follows.
func TestRunCommitMutants_ASurvivorOfItsCoveringTestsIsNotRunAgainstTheRestOfThePackage(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })

	got := runCommitOnce(t, root, planFor(mapFor("Kind", "TestKind_A")), kindMutant, time.Minute)

	if got.Outcome.Status != "missed" || got.NotMeasured != "" {
		t.Fatalf("outcome = %q, not measured %q, want missed", got.Outcome.Status, got.NotMeasured)
	}
	if s.count() != 1 || s.calls[0].Run != "^(TestKind_A)$" {
		t.Errorf("go test calls = %+v, want one, over the covering test only", s.calls)
	}
	if got.WholePackage {
		t.Error("the whole package was run for a survivor of an exact selection")
	}
}

func TestCommitReport_NotCoveredMutantsAreCountedUnderTheirOwnName(t *testing.T) {
	t.Parallel()
	runs := []commitRun{
		{NotMeasured: "x", GapKind: gapNotCovered},
		{NotMeasured: "x", GapKind: gapNotCovered},
		{NotMeasured: "y", GapKind: gapBudget},
	}
	_, measured, gaps := commitReport(MutantsConfig{}, runs)
	if measured != 0 || gaps != "1 budget, 2 not-covered" {
		t.Errorf("measured %d gaps %q, want 0 and \"1 budget, 2 not-covered\"", measured, gaps)
	}
}

// coverFixture is a package the coverage build can run on the fake toolchain:
// internal/p with Test_A executing line 4 and Test_B lines 4 and 8.
// coverFixtureMutants are a mutant in each of the fixture's two functions.
var coverFixtureMutants = []commitMutant{{File: "internal/p/p.go", Line: 4}, {File: "internal/p/p.go", Line: 8}}

func coverFixture(t *testing.T) (string, *fakeToolchain) {
	t.Helper()
	tc := &fakeToolchain{
		list:     "Test_B\nTest_A\n",
		profiles: map[string]string{"Test_A": profileF, "Test_B": profileFG},
	}
	return buildFixture(t, tc), tc
}

func testRuns(tc *fakeToolchain) (compiles, soloRuns int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	for _, argv := range tc.calls {
		switch {
		case argv[0] == "go":
			compiles++
		case prefixValue(argv, "-test.run=") != "":
			soloRuns++
		}
	}
	return compiles, soloRuns
}

// One compile and one solo run of each test is the whole cost of the map.
func TestMeasureTestMaps_OneCompileAndOneSoloRunPerTestFillsThePackagesMap(t *testing.T) {
	root, tc := coverFixture(t)
	plans := map[string]*commitPlan{"internal/p": {Dir: "internal/p"}}

	measureTestMaps(context.Background(), root, MutantsConfig{}, plans, coverFixtureMutants, 2, nil, io.Discard)

	m := plans["internal/p"].Map
	if m == nil {
		t.Fatal("the package got no map")
	}
	if got, _ := m.testsAt("p.go", 8); !slices.Equal(got, []string{"Test_B"}) {
		t.Errorf("testsAt(p.go, 8) = %v, want [Test_B]", got)
	}
	if compiles, solo := testRuns(tc); compiles != 1 || solo != 2 {
		t.Errorf("%d compiles and %d solo runs, want 1 and 2", compiles, solo)
	}
}

// A package whose tests are all run anyway (a changed TestMain) gets no map
// and costs no run.
func TestMeasureTestMaps_APackageRunWholeAnywayIsNotMeasured(t *testing.T) {
	root, tc := coverFixture(t)
	plans := map[string]*commitPlan{"internal/p": {Dir: "internal/p", Whole: true}}

	measureTestMaps(context.Background(), root, MutantsConfig{}, plans, coverFixtureMutants, 2, nil, io.Discard)

	if plans["internal/p"].Map != nil {
		t.Error("a package run whole was given a map")
	}
	if len(tc.calls) != 0 {
		t.Errorf("%d commands ran, want none", len(tc.calls))
	}
}

// A package that cannot be measured is named NOT MEASURED and keeps no map: its
// mutants fall back to the commit's touched tests and then the whole package.
func TestMeasureTestMaps_AFailedBuildIsNamedAndLeavesNoMap(t *testing.T) {
	root, tc := coverFixture(t)
	tc.compile = func([]string) (int, error) { return 2, nil }
	plans := map[string]*commitPlan{"internal/p": {Dir: "internal/p"}}
	var log strings.Builder

	measureTestMaps(context.Background(), root, MutantsConfig{}, plans, coverFixtureMutants, 1, nil, &log)

	if plans["internal/p"].Map != nil {
		t.Error("a failed build left a map")
	}
	if !strings.Contains(log.String(), "coverage of internal/p NOT MEASURED") {
		t.Errorf("log = %q, want the package named NOT MEASURED", log.String())
	}
}

// The stage end to end on the fake toolchain: the coverage run happens once,
// and each mutant runs only the tests that execute its line.
func TestMutantsAtCommitStage_RunsEachMutantAgainstTheTestsThatExecuteItsLine(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, root := commitStage(t, "")
	tc := &fakeToolchain{
		list:     "TestKind_A\nTestKind_B\nTestOther\n",
		profiles: map[string]string{"TestKind_A": "mode: set\nx/gate/gate.go:4.2,4.12 1 1\n", "TestKind_B": "mode: set\nx/gate/gate.go:4.2,4.12 1 1\n", "TestOther": "mode: set\nx/gate/gate.go:11.2,11.14 1 1\n"},
	}
	prevExec := testMapExecFn
	testMapExecFn = tc.exec
	t.Cleanup(func() { testMapExecFn = prevExec })
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })

	stderr := captureStderr(t, func() { mutantsAtCommitStage("precommit", root) })

	if compiles, solo := testRuns(tc); compiles != 1 || solo != 3 {
		t.Errorf("coverage cost %d compiles and %d solo runs, want 1 and 3", compiles, solo)
	}
	if s.count() != 2 {
		t.Fatalf("go test ran %d times, want one per mutant:\n%s", s.count(), stderr)
	}
	for _, c := range s.calls {
		if c.Run != "^(TestKind_A|TestKind_B)$" {
			t.Errorf("a mutant ran -run %q, want the two tests that execute its line", c.Run)
		}
	}
}
