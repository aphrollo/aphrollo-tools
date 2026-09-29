package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

func manyPackages(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("./%s/p%03d", strings.Repeat("d", 60), i))
	}
	return out
}

// TestRunBatched_WindowsCmdBudgetSplitsTwoHundredPackagesAcrossRuns pins the
// gate's one executor on the shape that broke a merge commit: a `go test`
// over 200 long package paths, held to cmd.exe's budget, runs as several
// commands, each within it, that between them name every package once.
func TestRunBatched_WindowsCmdBudgetSplitsTwoHundredPackagesAcrossRuns(t *testing.T) {
	pkgs := manyPackages(200)
	r := Runner{Cmd: "go", Args: append([]string{"test"}, pkgs...)}
	var lines []int
	var seen []string
	run := func(b Runner, _ string) SuiteResult {
		lines = append(lines, len(b.Cmd+" "+strings.Join(b.Args, " ")))
		seen = append(seen, b.Args[1:]...)
		return SuiteResult{Passed: true, Output: "ok\n", Duration: time.Second}
	}

	got := runBatched(r, "root", time.Minute, 6000, run)

	if len(lines) < 3 {
		t.Fatalf("%d runs for 200 packages of 65 chars at a 6000-char budget, want at least 3", len(lines))
	}
	for i, n := range lines {
		if n > 6000 {
			t.Errorf("run %d is %d chars, past the 6000-char budget", i, n)
		}
	}
	if !slices.Equal(seen, pkgs) {
		t.Fatalf("packages across runs = %d, want the %d given once each in order", len(seen), len(pkgs))
	}
	if !got.Passed || got.Output != strings.Repeat("ok\n", len(lines)) || got.Duration != time.Duration(len(lines))*time.Second {
		t.Fatalf("merged result = %+v, want passed, every run's output, and their durations summed", got)
	}
}

// TestRunBatched_StopsAtTheFirstRunThatDoesNotPass pins that a red batch is
// the verdict: its own result comes back after the batches before it, and no
// later batch runs.
func TestRunBatched_StopsAtTheFirstRunThatDoesNotPass(t *testing.T) {
	r := Runner{Cmd: "go", Args: append([]string{"test"}, manyPackages(200)...)}
	calls := 0
	run := func(b Runner, _ string) SuiteResult {
		calls++
		if calls == 2 {
			return SuiteResult{Passed: false, Output: "FAIL\n", Err: "exit status 1"}
		}
		return SuiteResult{Passed: true, Output: "ok\n"}
	}

	got := runBatched(r, "root", time.Minute, 6000, run)

	if calls != 2 {
		t.Fatalf("%d runs, want the run to stop at the failing second", calls)
	}
	if got.Passed || got.Output != "ok\nFAIL\n" || got.Err != "exit status 1" {
		t.Fatalf("merged result = %+v, want the failure after the first run's output", got)
	}
}

// TestRunBatched_ATimeoutEndsTheRunAndSharesOneBudget pins the deadline: the
// batches share the caller's one timeout instead of each taking a full one,
// and a run that timed out reports as timed out with no later run.
func TestRunBatched_ATimeoutEndsTheRunAndSharesOneBudget(t *testing.T) {
	r := Runner{Cmd: "go", Args: append([]string{"test"}, manyPackages(200)...)}
	var deadlines []time.Time
	run := func(b Runner, _ string) SuiteResult {
		deadlines = append(deadlines, b.Deadline)
		if len(deadlines) == 2 {
			return SuiteResult{TimedOut: true}
		}
		return SuiteResult{Passed: true}
	}
	before := time.Now()

	got := runBatched(r, "root", time.Minute, 6000, run)

	if len(deadlines) != 2 || !got.TimedOut || got.Passed {
		t.Fatalf("runs=%d result=%+v, want a timeout to end the run after two", len(deadlines), got)
	}
	for i, d := range deadlines {
		if d.Before(before.Add(time.Minute-time.Second)) || d.After(time.Now().Add(time.Minute)) {
			t.Errorf("run %d deadline %v is not the shared one-minute budget", i, d)
		}
	}
}

// TestRunBatched_AnEarlierRunnerDeadlineWins pins that a deadline the runner
// already carries, earlier than the timeout, is the one every batch keeps.
func TestRunBatched_AnEarlierRunnerDeadlineWins(t *testing.T) {
	early := time.Now().Add(10 * time.Second)
	r := Runner{Cmd: "go", Args: append([]string{"test"}, manyPackages(200)...), Deadline: early}
	run := func(b Runner, _ string) SuiteResult {
		if !b.Deadline.Equal(early) {
			t.Errorf("batch deadline = %v, want the runner's own %v", b.Deadline, early)
		}
		return SuiteResult{Passed: true}
	}
	runBatched(r, "root", time.Minute, 6000, run)
}

// TestRunBatched_ALineWithinBudgetIsOneUntouchedRun pins that nothing is
// rewritten for a call that fits: one run, the runner as given.
func TestRunBatched_ALineWithinBudgetIsOneUntouchedRun(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./a", "./b"}}
	calls := 0
	run := func(b Runner, _ string) SuiteResult {
		calls++
		if !slices.Equal(b.Args, r.Args) || !b.Deadline.IsZero() {
			t.Errorf("run got %+v, want the runner unchanged", b)
		}
		return SuiteResult{Passed: true}
	}
	runBatched(r, "root", time.Minute, 6000, run)
	if calls != 1 {
		t.Fatalf("%d runs, want 1", calls)
	}
}

// TestRunSuite_SplitsARealCommandThatIsTooLongAndJoinsItsOutput runs the real
// executor with the budget forced down: `go list` over two packages runs as
// two commands and the caller reads both packages' lines in one output.
func TestRunSuite_SplitsARealCommandThatIsTooLongAndJoinsItsOutput(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/m\n\ngo 1.21\n")
	write("a/a.go", "package a\n")
	write("b/b.go", "package b\n")
	defer func(prev func(string) int) { argvBudgetFn = prev }(argvBudgetFn)
	argvBudgetFn = func(string) int { return len("go list ./a ./b") - 1 }

	got := RunSuite(time.Minute)(Runner{Cmd: "go", Args: []string{"list", "./a", "./b"}}, dir)

	if !got.Passed || got.Output != "example.test/m/a\nexample.test/m/b\n" {
		t.Fatalf("RunSuite = passed %v output %q err %q, want both packages listed", got.Passed, got.Output, got.Err)
	}
}

// TestRunSuite_TwoHundredLongPackagesUnderTheWindowsCmdShimBudget is the
// lint and test package list of a large merge commit: the budget a `go` that
// resolves to a .cmd shim gets on Windows, applied on any platform, splits
// two hundred long package paths into runs that each stay within it, and the
// caller still reads every package once.
func TestRunSuite_TwoHundredLongPackagesUnderTheWindowsCmdShimBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var pkgs []string
	for i := range 200 {
		rel := fmt.Sprintf("%s/p%03d", strings.Repeat("d", 60), i)
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel, "p.go"), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, "./"+rel)
	}
	defer func(prev func(string) int) { argvBudgetFn = prev }(argvBudgetFn)
	argvBudgetFn = func(cmd string) int {
		return argvbatch.BudgetOn("windows", cmd, func(string) (string, error) { return `C:\shims\go.cmd`, nil })
	}
	r := Runner{Cmd: "go", Args: append([]string{"list"}, pkgs...)}
	if n := len(cmdString(r)); n < 12000 {
		t.Fatalf("fixture line is %d chars, want past cmd.exe's 8191", n)
	}

	got := RunSuite(2*time.Minute)(r, dir)

	if !got.Passed {
		t.Fatalf("RunSuite failed: %s %s", got.Err, got.Output)
	}
	if lines := strings.Count(got.Output, "\n"); lines != 200 {
		t.Fatalf("%d packages listed, want 200, each once", lines)
	}
}
