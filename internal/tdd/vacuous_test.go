package tdd

// This file covers #317: the suite/fail-first stages reject a Go run that
// exited 0 having executed zero tests — RunSuite's own -v injection, the
// runSuiteStage/failFirstStage blocks, and the goRunIsVacuous classifier
// underneath both.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunSuite_GoTestOutputCanBeJudgedByGoRunIsVacuous pins the production
// wiring #317 depends on: RunSuite must run a Go test invocation verbosely
// enough that goRunIsVacuous can tell "ran and executed nothing" (a TestMain
// that returns before calling m.Run(), the #194 shape) from "ran and passed
// normally" — plain (non -v) `go test` output carries no such signal at all,
// so without this the suite/fail-first stages would have nothing to count.
func TestRunSuite_GoTestOutputCanBeJudgedByGoRunIsVacuous(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/vacuous\n\ngo 1.21\n")
	write(t, dir, "widget.go", "package m\n")
	write(t, dir, "widget_test.go", "package m\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestMain(m *testing.M) {\n\tos.Exit(0)\n}\n\n"+
		"func TestWidget(t *testing.T) { t.Fatal(\"must never run\") }\n")

	res := RunSuite(30*time.Second)(Runner{Cmd: "go", Args: []string{"test", "."}}, dir)
	if !res.Passed {
		t.Fatalf("setup: expected the TestMain-exit(0) binary to exit 0, got Passed=false output=%q err=%q", res.Output, res.Err)
	}
	if !goRunIsVacuous(res.Output) {
		t.Fatalf("goRunIsVacuous saw nothing to catch in RunSuite's own output: %q — RunSuite must run `go test` verbosely enough to count", res.Output)
	}
}

// TestRunSuite_GoTestWithRealPassingTestsIsNeverVacuous is the base case:
// RunSuite's added verbosity must not manufacture a false vacuous reading
// for a package whose tests genuinely ran.
func TestRunSuite_GoTestWithRealPassingTestsIsNeverVacuous(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/vacuous\n\ngo 1.21\n")
	write(t, dir, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, dir, "widget_test.go", "package m\n\nimport \"testing\"\n\n"+
		"func TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")

	res := RunSuite(30*time.Second)(Runner{Cmd: "go", Args: []string{"test", "."}}, dir)
	if !res.Passed {
		t.Fatalf("setup: expected a real passing test to exit 0, got Passed=false output=%q err=%q", res.Output, res.Err)
	}
	if goRunIsVacuous(res.Output) {
		t.Fatalf("goRunIsVacuous(%q) = true, want false — a real test ran", res.Output)
	}
}

// vacuousGoOutput is the shape RunSuite's own `-v` output takes for the #194
// bug: the package built and the binary exited 0, but nothing behind it ran.
const vacuousGoOutput = "ok  \texample.com/m\t0.004s\n"

// TestPrecommit_RejectsAGoSuiteThatExecutedZeroTests is #317's suite-stage
// half: a mechanical run that exits 0 having executed zero Go tests must
// block the commit — a RED proof or a real green with zero tests behind it
// is nothing was tested, not a pass — and its gate.log entry must carry its
// own token, distinct from a real failure or a timeout, so `gate stats` can
// count it separately.
func TestPrecommit_RejectsAGoSuiteThatExecutedZeroTests(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestMain(m *testing.M) {\n\tos.Exit(0)\n}\n")
	gitDo(t, root, "add", ".")

	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: vacuousGoOutput}
	}

	res := Precommit(root, run)
	if !res.Blocked {
		t.Fatal("a suite that executed zero tests despite exiting 0 must block the commit, not pass")
	}

	logData, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(logData), "vacuous-rejected") {
		t.Fatalf("expected gate.log to carry the vacuous-rejected token, got:\n%s", logData)
	}
}

// TestFailFirstStage_RejectsAGoProofThatExecutedZeroTests is #317's
// fail-first half: the worktree run that is supposed to PROVE the new test
// goes RED without the new source instead executed zero tests (a narrowed
// -run filter matching nothing, say) — that is not a red proof and not a
// conclusive violation either, so it needs its own name rather than being
// misreported as "your test passed without the implementation".
func TestFailFirstStage_RejectsAGoProofThatExecutedZeroTests(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: vacuousGoOutput}
	}

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)
	})
	if !res.Blocked {
		t.Fatalf("a fail-first proof that executed zero tests must block, got: %+v", res)
	}
	if !strings.Contains(stderr, "vacuous") {
		t.Fatalf("expected the fail-first stderr line to name the vacuous outcome, got: %s", stderr)
	}

	logData, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(logData), "vacuous-rejected") {
		t.Fatalf("expected gate.log to carry the vacuous-rejected token, got:\n%s", logData)
	}
}
