package precommit

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// twentyPackageMerge is a Go repo where merging lane/work into trunk brings in
// twenty new packages, left staged for the merge gate to judge.
func twentyPackageMerge(t *testing.T) string {
	t.Helper()
	lane := map[string]string{}
	for i := range 20 {
		lane[fmt.Sprintf("internal/p%02d/p%02d.go", i, i)] = fmt.Sprintf("package p%02d\n\nfunc F() int { return %d }\n", i, i)
	}
	root, trunk := syncRepo(t, lane, map[string]string{"README.md": "# readme\n"})
	gitDo(t, root, "checkout", "-q", trunk)
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "lane/work")
	return root
}

// goTestCalls is a suite runner that answers every `go test` command through
// answer (the quality runners pass) and keeps the commands it was given, in
// the order they were started.
type goTestCalls struct {
	mu     sync.Mutex
	runs   []Runner
	answer func(call int, r Runner) SuiteResult
}

func (c *goTestCalls) run(r Runner, _ string) SuiteResult {
	if isQualityRunner(r) {
		return SuiteResult{Passed: true}
	}
	c.mu.Lock()
	call := len(c.runs)
	c.runs = append(c.runs, r)
	c.mu.Unlock()
	return c.answer(call, r)
}

func testedPackages(r Runner) []string {
	var out []string
	for _, a := range r.Args {
		if strings.HasPrefix(a, "./") {
			out = append(out, a)
		}
	}
	return out
}

// TestMechanical_ATwentyPackageMergeIsRunAsSeveralRunsAndEachPackageOnce pins
// the merge gate's answer to a list too big for one budget: twenty packages
// with no recorded time are cut into runs that each fit, every package is in
// exactly one run with the -race -shuffle flags the merge always had, and the
// merge passes when every run does.
// Serial: sets APHROLLO_MECH_PARALLEL so the runs go one after another.
func TestMechanical_ATwentyPackageMergeIsRunAsSeveralRunsAndEachPackageOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_MECH_PARALLEL", "1")
	root := twentyPackageMerge(t)
	calls := &goTestCalls{answer: func(_ int, _ Runner) SuiteResult { return SuiteResult{Passed: true, Duration: time.Second} }}

	res := Mechanical(root, calls.run)

	if res.Blocked {
		t.Fatalf("a merge whose every run passes must not be blocked: %s", res.Message)
	}
	if len(calls.runs) < 2 {
		t.Fatalf("%d go test run for twenty packages, want them cut into several", len(calls.runs))
	}
	var seen []string
	for _, r := range calls.runs {
		if !slices.Equal(r.Args[:4], []string{"test", "-race", "-count=1", "-shuffle=on"}) {
			t.Errorf("run %v lost its flags: %v", testedPackages(r), r.Args)
		}
		seen = append(seen, testedPackages(r)...)
	}
	slices.Sort(seen)
	var want []string
	for i := range 20 {
		want = append(want, fmt.Sprintf("./internal/p%02d", i))
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("packages across runs = %v, want the twenty once each", seen)
	}
}

// TestMechanical_ASplitMergeWhoseRunTimesOutIsRefusedNamingThePackages pins the
// inconclusive verdict end to end: one run of the cut list never finishes, so
// the merge is refused (never passed), the refusal names that run's packages
// and does not claim nothing was tested, since the runs before it were.
// Serial: sets APHROLLO_MECH_PARALLEL so the runs go one after another.
func TestMechanical_ASplitMergeWhoseRunTimesOutIsRefusedNamingThePackages(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_MECH_PARALLEL", "1")
	root := twentyPackageMerge(t)
	calls := &goTestCalls{answer: func(call int, _ Runner) SuiteResult {
		if call == 1 {
			return SuiteResult{TimedOut: true, Duration: 600 * time.Second}
		}
		return SuiteResult{Passed: true, Duration: time.Second}
	}}

	res := Mechanical(root, calls.run)

	if !res.Blocked {
		t.Fatalf("a merge with a run that never finished must be refused, got %q", res.Message)
	}
	if len(calls.runs) != 2 {
		t.Fatalf("%d runs started, want the third and later to stay unstarted after the second timed out", len(calls.runs))
	}
	wantRun := "[" + strings.Join(testedPackages(calls.runs[1]), " ") + "] after 600s"
	if !strings.Contains(res.Message, wantRun) {
		t.Errorf("message = %q, want it to name the timed-out run's packages: %s", res.Message, wantRun)
	}
	if !strings.Contains(res.Message, "not every package was tested") || strings.Contains(res.Message, "nothing was tested") {
		t.Errorf("message = %q, want it to say not every package was tested, not that nothing was", res.Message)
	}
	if !strings.Contains(res.Message, "not started: run 3 of ") {
		t.Errorf("message = %q, want it to name the runs that never started", res.Message)
	}
	if log := gateLogHere(t); !strings.Contains(log, "timeout-rejected") {
		t.Errorf("gate.log = %q, want the timeout-rejected verdict", log)
	}
}
