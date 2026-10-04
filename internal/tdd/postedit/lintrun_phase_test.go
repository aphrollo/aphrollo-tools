package postedit

import (
	"encoding/json"
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

// sleepCmd is a command that outlives any lint budget a test sets.
func sleepCmd() []string {
	if runtime.GOOS == "windows" {
		return []string{"ping", "-n", "30", "127.0.0.1"}
	}
	return []string{"sleep", "30"}
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

func notLoaded(t *testing.T, loaded bool) {
	t.Helper()
	prev := lintEditLoad
	lintEditLoad = func() (float64, int, bool) {
		if loaded {
			return 100, 1, true
		}
		return 0, 8, true
	}
	t.Cleanup(func() { lintEditLoad = prev })
}

// The lint rides the run's own wrapper after the tests: the test's exit code is
// the test's alone, whatever the linter said, and the lint's output is a log of
// its own.
func TestRunPhase_LintFindingsLeaveTheTestExitCodeAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	notLoaded(t, false)
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
	data, err := os.ReadFile(lintDonePath(j, out.RunID))
	var done1 lintDone
	if err != nil || json.Unmarshal(data, &done1) != nil || done1.Exit != 1 {
		t.Fatalf("lint record = %q (%v), want exit 1", data, err)
	}
	logged, err := os.ReadFile(lintLogPath(j, out.RunID))
	if err != nil || !strings.Contains(string(logged), "x.go:3:1: unused thing (unused)") {
		t.Fatalf("lint log = %q (%v), want the finding", logged, err)
	}
	testLog, _ := os.ReadFile(j.Log)
	if strings.Contains(string(testLog), "unused thing") {
		t.Fatalf("test log = %q, the lint output must not mix into the test's own log", testLog)
	}
}

// The test verdict never waits on the lint: when the lint starts, the result
// file and the store verdict exist and the run's slot is free again.
func TestRunPhase_TheVerdictIsWrittenAndTheSlotFreeBeforeTheLintStarts(t *testing.T) {
	root, _ := laneAt(t, "eee555")
	withIsolatedBuildLock(t)
	notLoaded(t, false)
	j := lintJob(t, root, writeMarkerCmd(filepath.Join(root, "ran")))
	var sawResult, sawStore, slotFree bool
	prev := phaseLintFn
	phaseLintFn = func(DeferredJob) (Runner, bool) {
		_, sawResult = deferredResult(j)
		if s, err := openRunStoreFn(root); err == nil {
			_, sawStore, _ = s.ReadVerdict("eee555")
		}
		var releases []func()
		slotFree = true
		for range buildSlotCount() {
			_, release, ok := TryAcquireBuildSlot(ResolveCargoTargetDir(t.TempDir()), "probe", "/repo")
			if !ok {
				slotFree = false
				break
			}
			releases = append(releases, release)
		}
		for _, r := range releases {
			r()
		}
		return Runner{}, false
	}
	t.Cleanup(func() { phaseLintFn = prev })

	RunPhase(writeJob(t, j))

	if !sawResult || !sawStore || !slotFree {
		t.Fatalf("at the lint's start: result written %v, store verdict written %v, slot free %v; want all three", sawResult, sawStore, slotFree)
	}
}

// A loaded box and a box that recently timed a lint out are left alone.
func TestRunPhase_ALoadedOrBackedOffBoxRunsNoLint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"loaded", func(t *testing.T, _ string) { notLoaded(t, true) }},
		{"backed off", func(t *testing.T, root string) { notLoaded(t, false); markLintBackoff(root) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			withIsolatedBuildLock(t)
			dir := t.TempDir()
			tc.setup(t, dir)
			stubPhaseLint(t, lintFindingCmd("x.go:3:1: unused thing (unused)"))
			j := lintJob(t, dir, writeMarkerCmd(filepath.Join(dir, "ran")))

			RunPhase(writeJob(t, j))

			if lintFilesOf(j, ".lint") != 0 || lintFilesOf(j, ".lint.json") != 0 {
				t.Fatal("a lint ran on a box that should have stood down")
			}
		})
	}
}

// A lint past its budget is ended, leaves no record, and backs the next one off.
func TestRunPhase_ALintPastItsBudgetIsEndedAndBacksOff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	notLoaded(t, false)
	prev := lintPhaseBudget
	lintPhaseBudget = 300 * time.Millisecond
	t.Cleanup(func() { lintPhaseBudget = prev })
	dir := t.TempDir()
	stubPhaseLint(t, sleepCmd())
	j := lintJob(t, dir, writeMarkerCmd(filepath.Join(dir, "ran")))

	RunPhase(writeJob(t, j))

	if lintFilesOf(j, ".lint.json") != 0 {
		t.Fatal("a lint that did not finish left a record")
	}
	if !lintBackedOff(dir) {
		t.Fatal("a lint past its budget must back the next one off")
	}
}

// A run that asks for no lint records none, and removes what an earlier run of
// the same log left.
func TestRunPhase_NoLintWhenThePhaseAsksForNone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	stubPhaseLint(t, nil)
	j := lintJob(t, dir, writeMarkerCmd(filepath.Join(dir, "ran")))
	mustWrite(t, lintLogPath(j, "earlier"), "old.go:1:1: from an earlier run (x)\n")
	mustWrite(t, lintDonePath(j, "earlier"), `{"exit":1}`)

	RunPhase(writeJob(t, j))

	if lintFilesOf(j, ".lint") != 0 || lintFilesOf(j, ".lint.json") != 0 {
		t.Fatal("an earlier run's lint files outlived the run that started the log afresh")
	}
}

// The lint's packages are named from the module the run's own tests run in, and
// it runs there: a module below the project is not the project.
func TestGoPhaseLint_NamesPackagesFromTheModuleTheRunsIn(t *testing.T) {
	prev := lintEditLook
	lintEditLook = func() bool { return true }
	t.Cleanup(func() { lintEditLook = prev })
	root := t.TempDir()
	module := filepath.Join(root, "svc")
	mustWrite(t, filepath.Join(module, "pkg", "x.go"), "package pkg\n")
	mustWrite(t, filepath.Join(root, "top", "y.go"), "package top\n")
	j := DeferredJob{
		Project: root, Dir: module, Phase: "run", Runner: []string{"go", "test", "./pkg"},
		File: filepath.Join(module, "pkg", "x.go"), Touched: []string{filepath.Join(root, "top", "y.go")},
	}

	r, ok := goPhaseLint(j)

	want := []string{"run", "--allow-serial-runners", "./pkg"}
	if !ok || r.Dir != module || !slices.Equal(r.Args, want) {
		t.Fatalf("runner = %s %q in %q (%v), want %q in %q: only files of the module, named from it", r.Cmd, r.Args, r.Dir, ok, want, module)
	}
}

// lintFilesOf counts the lint files of any run of the job's log that end in suffix.
func lintFilesOf(j DeferredJob, suffix string) int {
	dir, prefix := filepath.Split(j.Log)
	entries, _ := os.ReadDir(dir) // an unreadable directory holds none
	n := 0
	for _, e := range entries {
		if name := e.Name(); strings.HasPrefix(name, prefix+".") && strings.HasSuffix(name, suffix) {
			n++
		}
	}
	return n
}

// lintFinished leaves the lint files of run id as a finished lint does.
func lintFinished(t *testing.T, j DeferredJob, id string, exit int, log string) {
	t.Helper()
	mustWrite(t, lintLogPath(j, id), log)
	data, err := json.Marshal(lintDone{Exit: exit})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, lintDonePath(j, id), string(data))
}

// A lint finding rides a harvested green run's line as guidance: the verdict
// stays green and the finding is named on the same line.
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
	lintFinished(t, j, "run1", 1, "x.go:3:1: unused thing (unused)\nx.go:9:2: loading failed (typecheck)\n")
	writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 1, RunID: "run1"})

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

// ratchet: test_removed TestLintGuidance_SaysNothingUnlessTheLinterReportedFindings: renamed TestLintGuidance_SaysNothingUnlessAFinishedLintReportedFindings; the lint now records its own finish

// Lint that found nothing, has not finished, or could not judge (exit 3: the
// linter's own lock) adds nothing to the line.
func TestLintGuidance_SaysNothingUnlessAFinishedLintReportedFindings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	finding := "x.go:3:1: unused thing (unused)\n"
	for name, exit := range map[string]int{"clean": 0, "contention": 3} {
		j := lintJob(t, t.TempDir(), []string{"go", "test"})
		lintFinished(t, j, "run1", exit, finding)
		if got := lintGuidance(j, "run1"); got != "" {
			t.Errorf("%s: guidance = %q, want none", name, got)
		}
	}
	unfinished := lintJob(t, t.TempDir(), []string{"go", "test"})
	mustWrite(t, lintLogPath(unfinished, "run1"), finding)
	if got := lintGuidance(unfinished, "run1"); got != "" {
		t.Errorf("unfinished: guidance = %q, want none: a lint still going says nothing", got)
	}
}

// The same guidance rides the line of a run that finishes inside the hook's own
// budget, and the stamped verdict is the tests' alone: it is recorded green.
func TestPostEdit_LintFindingRidesAnInBudgetRunsLineWithoutMovingItsVerdict(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("TRELLIS_DATA", t.TempDir())
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
		lintFinished(t, j, "run1", 1, "widget.go:3:1: unused thing (unused)\n")
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 1, RunID: "run1"})
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

// A run reads only its own lint: an earlier run's finished lint, still on disk
// because it was held open when the new run began, is never the new run's.
func TestLintGuidance_ANewRunNeverReadsAnEarlierRunsLint(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	j := lintJob(t, t.TempDir(), []string{"go", "test"})
	lintFinished(t, j, "earlier", 1, "x.go:3:1: unused thing (unused)\n")

	if got := lintGuidance(j, "newer"); got != "" {
		t.Fatalf("guidance = %q, want none: the earlier run's lint is not this run's", got)
	}
	if got := lintGuidance(j, "earlier"); got == "" {
		t.Fatal("setup: the earlier run's own lint must still read")
	}
}

// Two runs never share an id, even when the clock does not move between them.
func TestNewRunID_IsUniqueWithoutHelpFromTheClock(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id := newRunID()
		if seen[id] {
			t.Fatalf("id %q issued twice", id)
		}
		seen[id] = true
	}
}

// The deferred sweep takes the lint files of a run nobody harvested with the
// rest of its files.
func TestSweepDeferredJobs_RemovesAnOldRunsLintFiles(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := deferredDirPath()
	mustWrite(t, filepath.Join(dir, "p.log.old.lint.json"), `{"exit":1}`)
	mustWrite(t, filepath.Join(dir, "p.log.old.lint"), "x.go:1:1: m (x)\n")
	old := time.Now().Add(-2 * deferredJobMaxAge)
	for _, n := range []string{"p.log.old.lint.json", "p.log.old.lint"} {
		if err := os.Chtimes(filepath.Join(dir, n), old, old); err != nil {
			t.Fatal(err)
		}
	}

	sweepDeferredJobs(time.Now())

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the sweep left %d files, want the old run's lint files gone", len(entries))
	}
}
