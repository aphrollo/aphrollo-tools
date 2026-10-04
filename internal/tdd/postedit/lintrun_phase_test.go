package postedit

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// lintFindingCmd is a stand-in linter that prints one finding and exits 1, the
// way golangci-lint does.
func lintFindingCmd(finding string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo " + finding + "& exit /b 1"}
	}
	return []string{"sh", "-c", "echo '" + finding + "'; exit 1"}
}

func stubPhaseLint(t *testing.T, argv []string) {
	t.Helper()
	prev := phaseLintFn
	phaseLintFn = func(DeferredJob) (Runner, bool) {
		if argv == nil {
			return Runner{}, false
		}
		return runnerFromArgv(argv, ""), true
	}
	t.Cleanup(func() { phaseLintFn = prev })
}

func lintJob(t *testing.T, dir string, runner []string) DeferredJob {
	t.Helper()
	return DeferredJob{
		Project: dir, Phase: "run", Dir: dir, Runner: runner,
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json"),
	}
}

// The lint rides the run's own slot: it runs after the tests, in the same
// wrapper, and records beside the test outcome. The test's exit code is the
// test's alone, whatever the linter said.
func TestRunPhase_LintFindingsLeaveTheTestExitCodeAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	stubPhaseLint(t, lintFindingCmd("x.go:3:1: unused thing (unused)"))
	j := lintJob(t, dir, writeMarkerCmd(filepath.Join(dir, "ran")))

	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done {
		t.Fatal("no result written")
	}
	if out.ExitCode != 0 || out.SetupFailed || out.Inconclusive != "" {
		t.Fatalf("outcome = %+v, want the passing test run untouched by the lint", out)
	}
	if !out.LintRan || out.LintExit != 1 {
		t.Fatalf("outcome = %+v, want the lint recorded as run with exit 1", out)
	}
	logged, err := os.ReadFile(lintLogPath(j))
	if err != nil || !strings.Contains(string(logged), "x.go:3:1: unused thing (unused)") {
		t.Fatalf("lint log = %q (%v), want the finding", logged, err)
	}
	testLog, _ := os.ReadFile(j.Log)
	if strings.Contains(string(testLog), "unused thing") {
		t.Fatalf("test log = %q, the lint output must not mix into the test's own log", testLog)
	}
}

// A run that is not a Go run asks for no lint, and one whose root has no
// linter records none: nothing in the outcome claims a lint that did not run.
func TestRunPhase_NoLintWhenThePhaseAsksForNone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	stubPhaseLint(t, nil)
	j := lintJob(t, dir, writeMarkerCmd(filepath.Join(dir, "ran")))

	RunPhase(writeJob(t, j))

	out, _ := deferredResult(j)
	if out.LintRan {
		t.Fatalf("outcome = %+v, want no lint recorded", out)
	}
}

// A lint finding rides the run's line as guidance: the verdict stays green and
// the finding is named on the same line, so the hook still prints one line.
func TestHarvest_LintFindingRidesAGreenRunsLineAsGuidance(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	target := filepath.Join(root, "x.go")
	mustWrite(t, target, "package x\n")
	saveDeferredJob(DeferredJob{
		Project: root, Session: "lint-sess", Phase: "run", Dir: root, PID: 4242,
		Started: time.Now(), File: target,
		HeadSHA: headSHAFor(root), FileHash: sourceIdentity(root, target),
		Runner: []string{"go", "test", "./..."},
	})
	j, _ := loadDeferredJob("lint-sess", root)
	mustWrite(t, j.Log, "ok  \tx\t0.01s\n")
	mustWrite(t, lintLogPath(j), "x.go:3:1: unused thing (unused)\nx.go:9:2: loading failed (typecheck)\n")
	writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 1, LintRan: true, LintExit: 1})

	got := harvestSessionJobs("lint-sess")

	if len(got) != 1 {
		t.Fatalf("lines = %q, want exactly one", got)
	}
	line := got[0]
	if !strings.Contains(line, "x.go:3:1: unused thing (unused)") || !strings.Contains(line, "1 lint finding") {
		t.Errorf("line = %q, want the lint finding named", line)
	}
	if strings.Contains(line, "loading failed") {
		t.Errorf("line = %q, a typecheck line is the build's finding, not a lint one", line)
	}
	if strings.Contains(line, "RED") || !strings.Contains(line, "→ green") {
		t.Errorf("line = %q, want the run still judged green", line)
	}
	if strings.Contains(line, "\n") {
		t.Errorf("line = %q, want one line", line)
	}
}

// Lint that found nothing, was not run, or could not judge (exit 3: the
// linter's own lock) adds nothing to the line.
func TestLintGuidance_SaysNothingUnlessTheLinterReportedFindings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	j := lintJob(t, dir, []string{"go", "test"})
	mustWrite(t, lintLogPath(j), "x.go:3:1: unused thing (unused)\n")
	for name, out := range map[string]PhaseOutcome{
		"not run":    {},
		"clean":      {LintRan: true, LintExit: 0},
		"contention": {LintRan: true, LintExit: 3},
	} {
		if got := lintGuidance(j, out); got != "" {
			t.Errorf("%s: guidance = %q, want none", name, got)
		}
	}
}

// The same guidance rides the line of a run that finishes inside the hook's own
// budget, and the stamped verdict is the tests' alone: it is recorded green.
func TestPostEdit_LintFindingRidesAnInBudgetRunsLineWithoutMovingItsVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := mkProject(t, "go.mod")
	src := filepath.Join(root, "widget.go")
	mustWrite(t, src, "package widget\n")
	mustWrite(t, filepath.Join(root, "widget_test.go"), "package widget\n")
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		j.PID = 1000
		saveDeferredJob(j)
		j, _ = loadDeferredJob(j.Session, j.Project)
		mustWrite(t, j.Log, "ok  \twidget\t0.01s\n")
		mustWrite(t, lintLogPath(j), "widget.go:3:1: unused thing (unused)\n")
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 1, LintRan: true, LintExit: 1})
		return j, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })

	text := PostEdit(postPayload("Edit", src), fakeRun(true, "ok"))

	if !strings.Contains(text, "widget.go:3:1: unused thing (unused)") || !strings.Contains(text, "→ green") {
		t.Fatalf("line = %q, want a green run carrying the lint finding", text)
	}
	want := []string{string(Green)}
	if got := postEditVerdicts(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("gate.log recorded %v, want %v: lint must not move the verdict", got, want)
	}
}
