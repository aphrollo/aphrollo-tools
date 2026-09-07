package tdd

// This file covers #317: the suite/fail-first stages reject a Go run that
// exited 0 having executed zero tests IN SOME PACKAGE — RunSuite's own
// -json invocation, the runSuiteStage/failFirstStage blocks, and the
// vacuousGoPackages classifier underneath both.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ratchet: test_removed TestRunSuite_GoTestOutputCanBeJudgedByGoRunIsVacuous: goRunIsVacuous
// was replaced by vacuousGoPackages (per-package attribution, PR #411 review);
// renamed to TestRunSuite_GoTestJSONCanBeJudgedByVacuousGoPackages below.

// TestRunSuite_GoTestJSONCanBeJudgedByVacuousGoPackages pins the production
// wiring #317 depends on: RunSuite must run a Go test invocation with
// enough structure (GoTestJSON) that vacuousGoPackages can tell "ran and
// executed nothing" (a TestMain that returns before calling m.Run(), the
// #194 shape) from "ran and passed normally" — plain (non -json) `go test`
// output carries no such signal at all, so without this the suite/fail-first
// stages would have nothing to count.
func TestRunSuite_GoTestJSONCanBeJudgedByVacuousGoPackages(t *testing.T) {
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
	got, err := vacuousGoPackages(res.GoTestJSON)
	if err != nil {
		t.Fatalf("vacuousGoPackages error = %v, want nil", err)
	}
	if len(got) == 0 {
		t.Fatalf("vacuousGoPackages saw nothing to catch in RunSuite's own GoTestJSON (%q) — RunSuite must run `go test -json` for the package data to exist at all", res.GoTestJSON)
	}
}

// TestRunSuite_GoTestWithRealPassingTestsIsNeverVacuous is the base case:
// RunSuite's -json invocation must not manufacture a false vacuous reading
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
	got, err := vacuousGoPackages(res.GoTestJSON)
	if err != nil {
		t.Fatalf("vacuousGoPackages error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("vacuousGoPackages = %v, want none — a real test ran", got)
	}
}

// TestRunSuite_MultiPackageRunAttributesVacuousToTheRightPackage reproduces
// the PR #411 review finding with the REAL Go toolchain, end to end through
// RunSuite: a two-package module where pkgok has a genuine passing test and
// pkgvacuous's TestMain calls os.Exit(0) before m.Run(). `go test ./...`
// (DetectRunner's Go default) covers both packages in ONE invocation, and
// pkgok's real "--- PASS" must not hide pkgvacuous going quietly vacuous
// beside it.
func TestRunSuite_MultiPackageRunAttributesVacuousToTheRightPackage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module multipkg\n\ngo 1.21\n")
	write(t, dir, "pkgok/ok.go", "package pkgok\n\nfunc OK() int { return 1 }\n")
	write(t, dir, "pkgok/ok_test.go", "package pkgok\n\nimport \"testing\"\n\n"+
		"func TestOK(t *testing.T) {\n\tif OK() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	write(t, dir, "pkgvacuous/v.go", "package pkgvacuous\n")
	write(t, dir, "pkgvacuous/v_test.go", "package pkgvacuous\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestMain(m *testing.M) {\n\tos.Exit(0)\n}\n")

	res := RunSuite(30*time.Second)(Runner{Cmd: "go", Args: []string{"test", "./..."}}, dir)
	if !res.Passed {
		t.Fatalf("setup: expected the run to exit 0 (pkgok passes, pkgvacuous exits 0), got Passed=false output=%q err=%q", res.Output, res.Err)
	}
	got, err := vacuousGoPackages(res.GoTestJSON)
	if err != nil {
		t.Fatalf("vacuousGoPackages error = %v, want nil", err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0], "pkgvacuous") {
		t.Fatalf("vacuousGoPackages = %v, want exactly [multipkg/pkgvacuous] — pkgok's real pass must not hide pkgvacuous", got)
	}
}

// vacuousPkgJSONLine is the GoTestJSON shape a stub SuiteRunner hands the
// gate for the #194 bug: the package built and the binary exited 0, but
// nothing behind it ran.
const vacuousPkgJSONLine = `{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

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
		return SuiteResult{Passed: true, Output: "ok  \texample.com/m\t0.004s\n", GoTestJSON: vacuousPkgJSONLine}
	}

	res := Mechanical(root, run)
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

// TestPrecommit_NamesTheVacuousPackageAmongInnocentSiblings pins the
// per-package attribution at the actual blocking stage (not just the unit
// classify_test.go coverage): a multi-package GoTestJSON stream must name
// only the package that went vacuous, never the one that genuinely ran.
func TestPrecommit_NamesTheVacuousPackageAmongInnocentSiblings(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestMain(m *testing.M) {\n\tos.Exit(0)\n}\n")
	gitDo(t, root, "add", ".")

	multiPkgJSON := `{"Action":"output","Package":"multipkg/pkgok","Output":"--- PASS: TestOK (0.00s)\n"}
{"Action":"pass","Package":"multipkg/pkgok","Test":"TestOK","Elapsed":0}
{"Action":"output","Package":"multipkg/pkgok","Output":"ok  \tmultipkg/pkgok\t0.104s\n"}
{"Action":"pass","Package":"multipkg/pkgok","Elapsed":0.105}
{"Action":"output","Package":"multipkg/pkgvacuous","Output":"ok  \tmultipkg/pkgvacuous\t0.087s\n"}
{"Action":"pass","Package":"multipkg/pkgvacuous","Elapsed":0.087}
`
	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: "ok\n", GoTestJSON: multiPkgJSON}
	}

	res := Mechanical(root, run)
	if !res.Blocked {
		t.Fatal("a run with any vacuous package must block the commit")
	}
	if !strings.Contains(res.Message, "pkgvacuous") {
		t.Fatalf("expected the block message to name pkgvacuous, got: %s", res.Message)
	}
	if strings.Contains(res.Message, "pkgok,") || strings.HasSuffix(strings.TrimSpace(res.Message), "pkgok") {
		t.Fatalf("expected the block message to NOT accuse pkgok, which genuinely ran, got: %s", res.Message)
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
		return SuiteResult{Passed: true, Output: "ok  \texample.com/m\t0.004s\n", GoTestJSON: vacuousPkgJSONLine}
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
