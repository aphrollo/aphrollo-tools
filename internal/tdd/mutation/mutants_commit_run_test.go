package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The commit-time runner applies each mutant through `go test -overlay` in a
// disposable copy of the lane and runs the tests selected for its function.
// These tests script `go test`, so what is under test is which runs the
// runner makes, in what order, and what it concludes from each answer.

const commitGateSource = `package gate

func Kind(n int) string {
	if n > 10 {
		return "big"
	}
	return "small"
}

func Label(s string) string {
	return "#" + s
}
`

// kindMutant is the `>` of `n > 10`, and labelMutant the `+` of a string
// concatenation, which cannot compile as anything else.
var (
	kindMutant  = commitMutant{File: "gate/gate.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_BOUNDARY", Func: "Kind"}
	labelMutant = commitMutant{File: "gate/gate.go", Line: 11, Col: 13, Mutation: "ARITHMETIC_BASE", Func: "Label"}
)

// goCall is one `go test` the runner made.
type goCall struct {
	Overlay bool
	Run     string
	Args    []string
}

// scriptedGo replaces the `go test` spawn for one test, answers each call from
// script and records them all. A script answer is the exit code and the text
// go would have printed.
type scriptedGo struct {
	mu    sync.Mutex
	calls []goCall
}

func scriptGo(t *testing.T, script func(goCall) (int, string)) *scriptedGo {
	t.Helper()
	s := &scriptedGo{}
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, _ string, _ []string, argv []string, log io.Writer) (int, error) {
		call := goCall{Args: slices.Clone(argv), Overlay: slices.Contains(argv, "-overlay"), Run: valueAfter(argv, "-run")}
		s.mu.Lock()
		s.calls = append(s.calls, call)
		s.mu.Unlock()
		code, text := script(call)
		_, _ = io.WriteString(log, text)
		return code, ctx.Err()
	}
	t.Cleanup(func() { resolveExecFn = prev })
	return s
}

func (s *scriptedGo) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

const failedGate = "--- FAIL: TestKind_A\nFAIL\ngithub.com/x/gate\nFAIL\tgate\t0.1s\n"

func commitRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "gate/gate.go", commitGateSource)
	write(t, root, "gate/gate_test.go", "package gate\n")
	return root
}

func mapFor(fn string, tests ...string) *testMap {
	m := &testMap{Schema: testMapSchema, Package: "gate", Tests: []string{"TestKind_A", "TestKind_B", "TestOther"}, Funcs: map[string][]int{}}
	for _, name := range tests {
		m.Funcs[fn] = append(m.Funcs[fn], slices.Index(m.Tests, name))
	}
	return m
}

func planFor(m *testMap) map[string]*commitPlan {
	return map[string]*commitPlan{"gate": {Dir: "gate", Map: m, Current: []string{"TestKind_A", "TestKind_B", "TestOther"}}}
}

func runCommitOnce(t *testing.T, root string, plans map[string]*commitPlan, m commitMutant, budget time.Duration) commitRun {
	t.Helper()
	runs := runCommitMutants(context.Background(), root, MutantsConfig{}, plans, []commitMutant{m}, 1, budget, io.Discard)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	return runs[0]
}

// A selection that kills is confirmed by the same run without the mutant, and
// the selected tests are the only ones run.
func TestRunCommitMutants_ASelectionThatKillsIsCaught(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, failedGate
		}
		return 0, "ok\tgate\n"
	})
	got := runCommitOnce(t, root, planFor(mapFor("Kind", "TestKind_A")), kindMutant, time.Minute)
	if got.Outcome.Status != "caught" || got.NotMeasured != "" {
		t.Fatalf("outcome = %q, not measured %q (%s), want caught", got.Outcome.Status, got.NotMeasured, got.Outcome.Note)
	}
	if got.WholePackage || !slices.Equal(got.Selected, []string{"TestKind_A"}) {
		t.Errorf("selected %v whole %v, want [TestKind_A] and not the whole package", got.Selected, got.WholePackage)
	}
	if s.count() != 2 {
		t.Fatalf("go test ran %d times, want 2 (the mutant, then the confirmation)", s.count())
	}
	first, second := s.calls[0], s.calls[1]
	if first.Run != "^(TestKind_A)$" || !first.Overlay {
		t.Errorf("first run = %+v, want the mutant under the selected -run", first)
	}
	if second.Overlay || second.Run != "^(TestKind_A)$" {
		t.Errorf("second run = %+v, want the same tests with no overlay", second)
	}
	for _, want := range []string{"-count=1", "-failfast", "./gate"} {
		if !slices.Contains(first.Args, want) {
			t.Errorf("argv %v lacks %q", first.Args, want)
		}
	}
}

// The confirmation that a failure is the mutant's runs only the tests that
// failed, which is one test in place of the whole selection.
func TestRunCommitMutants_TheConfirmationRunsOnlyTheFailingTests(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, "--- FAIL: TestKind_B (0.00s)\n    --- FAIL: TestKind_B/sub (0.00s)\nFAIL\ngate\nFAIL\tgate\t0.1s\n"
		}
		return 0, "ok\tgate\n"
	})
	got := runCommitOnce(t, root, planFor(mapFor("Kind", "TestKind_A", "TestKind_B")), kindMutant, time.Minute)
	if got.Outcome.Status != "caught" {
		t.Fatalf("outcome = %q (%s), want caught", got.Outcome.Status, got.NotMeasured)
	}
	if s.calls[0].Run != "^(TestKind_A|TestKind_B)$" || s.calls[1].Run != "^(TestKind_B)$" || s.calls[1].Overlay {
		t.Errorf("runs = %+v, want the selection under the mutant and then only TestKind_B without it", s.calls)
	}
}

// Each run records how long its mutant took, which is what the pass line
// reports as the slowest. The clock steps one second per reading.
func TestRunCommitMutants_RecordsHowLongEachMutantTook(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	var readings int
	prev := commitNowFn
	commitNowFn = func() time.Time {
		readings++
		return time.Unix(int64(readings), 0)
	}
	t.Cleanup(func() { commitNowFn = prev })
	got := runCommitOnce(t, root, planFor(nil), kindMutant, time.Minute)
	if got.Took != time.Second {
		t.Errorf("Took = %s, want the 1s between the two readings around the run", got.Took)
	}
}

func TestFailingTestNames_ReadsTopLevelFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		out  string
		want []string
	}{
		{"nothing failed", "ok\tgate\t0.1s\n", nil},
		{"empty", "", nil},
		{"one test", "--- FAIL: TestA (0.00s)\nFAIL\n", []string{"TestA"}},
		{"a subtest is its top-level test", "--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/x (0.00s)\n", []string{"TestA"}},
		{"two tests, sorted, once each", "--- FAIL: TestB (0s)\n--- FAIL: TestA (0s)\n--- FAIL: TestB (0s)\n", []string{"TestA", "TestB"}},
		{"a panic prints no failed test line", "panic: boom\nFAIL\tgate\t0.1s\n", nil},
		{"a skipped test is not a failure", "--- SKIP: TestA (0s)\n", nil},
	} {
		if got := failingTestNames(tc.out); !slices.Equal(got, tc.want) {
			t.Errorf("%s: failingTestNames = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The map is stale where it lists too few tests, so a mutant its selection
// misses gets the whole package before it is called a survivor.
func TestRunCommitMutants_ASelectionThatMissesFallsBackToTheWholePackage(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay && c.Run == "" {
			return 1, failedGate
		}
		return 0, "ok\tgate\n"
	})
	got := runCommitOnce(t, root, planFor(mapFor("Kind", "TestOther")), kindMutant, time.Minute)
	if got.Outcome.Status != "caught" || !got.WholePackage {
		t.Fatalf("outcome = %q whole %v (%s), want caught by the whole package", got.Outcome.Status, got.WholePackage, got.Outcome.Note)
	}
}

func TestRunCommitMutants_ASurvivorOfTheWholePackageIsMissed(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	got := runCommitOnce(t, root, planFor(mapFor("Kind", "TestKind_A")), kindMutant, time.Minute)
	if got.Outcome.Status != "missed" || got.NotMeasured != "" {
		t.Fatalf("outcome = %q, not measured %q, want missed", got.Outcome.Status, got.NotMeasured)
	}
	if !got.WholePackage || s.count() != 2 {
		t.Errorf("whole %v after %d runs, want the whole package run after the selection", got.WholePackage, s.count())
	}
	if !strings.Contains(got.Outcome.Note, "survived") {
		t.Errorf("note = %q, want it to say the mutant survived", got.Outcome.Note)
	}
}

// Without a map for the package there is nothing to select from: one run of
// the whole package, no -run.
func TestRunCommitMutants_NoMapRunsTheWholePackageOnce(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, failedGate
		}
		return 0, "ok\n"
	})
	got := runCommitOnce(t, root, planFor(nil), kindMutant, time.Minute)
	if got.Outcome.Status != "caught" || !got.WholePackage {
		t.Fatalf("outcome = %q whole %v, want caught by the whole package", got.Outcome.Status, got.WholePackage)
	}
	if s.calls[0].Run != "" {
		t.Errorf("run = %q, want no -run without a map", s.calls[0].Run)
	}
}

// A test the commit touched is run even when the map lists other tests only.
func TestRunCommitMutants_TouchedTestsJoinTheSelection(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	plans := planFor(mapFor("Kind", "TestKind_A"))
	plans["gate"].Touched = []string{"TestKind_B"}
	got := runCommitOnce(t, root, plans, kindMutant, time.Minute)
	if want := []string{"TestKind_A", "TestKind_B"}; !slices.Equal(got.Selected, want) {
		t.Errorf("selected %v, want %v", got.Selected, want)
	}
	if s.calls[0].Run != "^(TestKind_A|TestKind_B)$" {
		t.Errorf("run = %q, want both tests", s.calls[0].Run)
	}
}

// A changed TestMain means the tests cannot be told apart: the whole package.
func TestRunCommitMutants_ATestMainChangeRunsTheWholePackage(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	plans := planFor(mapFor("Kind", "TestKind_A"))
	plans["gate"].Whole = true
	got := runCommitOnce(t, root, plans, kindMutant, time.Minute)
	if !got.WholePackage || s.calls[0].Run != "" {
		t.Errorf("whole %v with -run %q, want the whole package", got.WholePackage, s.calls[0].Run)
	}
}

func TestRunCommitMutants_AMutantThatDoesNotCompileIsUnviable(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(goCall) (int, string) { return 1, "FAIL\tgate [build failed]\n" })
	got := runCommitOnce(t, root, planFor(nil), labelMutant, time.Minute)
	if got.Outcome.Status != "unviable" || got.NotMeasured != "" {
		t.Errorf("outcome = %q, not measured %q, want unviable", got.Outcome.Status, got.NotMeasured)
	}
}

// A failure that also happens without the mutant is not the mutant's kill, and
// nothing is claimed either way.
func TestRunCommitMutants_AFailureWithoutTheMutantIsNotAKill(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(goCall) (int, string) { return 1, failedGate })
	got := runCommitOnce(t, root, planFor(nil), kindMutant, time.Minute)
	if got.NotMeasured == "" || got.Outcome.Status == "caught" {
		t.Fatalf("outcome = %q, not measured %q, want it not measured", got.Outcome.Status, got.NotMeasured)
	}
	if !strings.Contains(got.NotMeasured, "without the mutant") {
		t.Errorf("reason = %q, want it to name the run without the mutant", got.NotMeasured)
	}
}

// A run still going when the wall-clock ends is cut and NOT MEASURED, never a
// survivor.
func TestRunCommitMutants_ARunPastTheBudgetIsNotMeasured(t *testing.T) {
	root := commitRoot(t)
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, _ string, _ []string, _ []string, _ io.Writer) (int, error) {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(30 * time.Second):
			return 0, nil
		}
	}
	t.Cleanup(func() { resolveExecFn = prev })
	got := runCommitOnce(t, root, planFor(nil), kindMutant, 2*time.Second) // the box may spend a fair part of it before the run starts
	if got.NotMeasured == "" || got.Outcome.Status == "missed" {
		t.Fatalf("outcome = %q, not measured %q, want NOT MEASURED", got.Outcome.Status, got.NotMeasured)
	}
	if !strings.Contains(got.NotMeasured, "did not finish") {
		t.Errorf("reason = %q, want it to say the tests did not finish", got.NotMeasured)
	}
}

// A budget that is already spent starts nothing.
func TestRunCommitMutants_ASpentBudgetStartsNothing(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
	got := runCommitOnce(t, root, planFor(nil), kindMutant, time.Nanosecond)
	if s.count() != 0 {
		t.Errorf("go test ran %d times on a spent budget", s.count())
	}
	if !strings.Contains(got.NotMeasured, "budget") {
		t.Errorf("reason = %q, want the budget named", got.NotMeasured)
	}
}

// Results come back in the order of the mutants whatever the worker count.
func TestRunCommitMutants_ResultsFollowTheInputOrder(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, failedGate
		}
		return 0, "ok\n"
	})
	mutants := []commitMutant{kindMutant, labelMutant, kindMutant, labelMutant, kindMutant}
	for _, workers := range []int{1, 2, 8} {
		runs := runCommitMutants(context.Background(), root, MutantsConfig{}, planFor(nil), mutants, workers, time.Minute, io.Discard)
		if len(runs) != len(mutants) {
			t.Fatalf("workers %d: %d runs, want %d", workers, len(runs), len(mutants))
		}
		for i, r := range runs {
			if r.Mutant != mutants[i] {
				t.Errorf("workers %d: run %d is for %+v, want %+v", workers, i, r.Mutant, mutants[i])
			}
		}
	}
}

func TestRunCommitMutants_NoMutantsRunNothing(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
	if runs := runCommitMutants(context.Background(), root, MutantsConfig{}, planFor(nil), nil, 4, time.Minute, io.Discard); len(runs) != 0 {
		t.Errorf("runs = %d, want none", len(runs))
	}
	if s.count() != 0 {
		t.Errorf("go test ran %d times for no mutants", s.count())
	}
}

// The mutation is applied in a copy: the lane's own file is never written.
func TestRunCommitMutants_LeavesTheLaneUntouched(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	_ = runCommitOnce(t, root, planFor(nil), kindMutant, time.Minute)
	data, err := os.ReadFile(filepath.Join(root, "gate", "gate.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != commitGateSource {
		t.Errorf("gate.go changed under the run:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(measureTempDir(root), "commit")); err == nil {
		t.Error("the run left its overlay directory behind")
	}
}
