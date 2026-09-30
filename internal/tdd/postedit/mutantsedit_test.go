package postedit

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The edit hook starts the commit-time mutation run over the lines an edit
// changed, detached, after an edit whose tests are green in a repo that
// declares mutants-at-commit, and the next hook of the session reports what it
// found. The spawn is a seam: no test here starts the real detached process.

const greenGoOutput = "ok  \tm\t0.003s\n"

func greenRun(Runner, string) SuiteResult {
	return SuiteResult{Passed: true, Output: greenGoOutput}
}

func redRun(Runner, string) SuiteResult {
	return SuiteResult{Passed: false, Output: "--- FAIL: TestWidget (0.00s)\nFAIL\nFAIL\tm\t0.003s\n"}
}

// mutantsEditFixture is a Go repository, declaring the run or not, with a
// source file to edit.
func mutantsEditFixture(t *testing.T, declare bool) (root, src string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root = makeGoRepo(t)
	if declare {
		write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n")
	}
	write(t, root, "widget.go", "package m\n\nfunc Widget(n int) bool { return n > 1 }\n")
	return root, filepath.Join(root, "widget.go")
}

// recordEditRunSpawns answers the jobs the hook asked to start. finish, when
// set, plays the detached process: it writes the log and the result at once.
func recordEditRunSpawns(t *testing.T, finish func(j mutantsEditJob)) *[]mutantsEditJob {
	t.Helper()
	var jobs []mutantsEditJob
	t.Cleanup(SetMutantsEditSpawnForTest(func(j mutantsEditJob) (int, bool) {
		jobs = append(jobs, j)
		if finish != nil {
			finish(j)
		}
		return 4242, true
	}))
	return &jobs
}

func finishWith(t *testing.T, status, log string) func(j mutantsEditJob) {
	t.Helper()
	return func(j mutantsEditJob) {
		if err := os.WriteFile(j.Log, []byte(log), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(j.Done, []byte(status+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostEdit_StartsTheMutationRunAfterAGreenGoEdit(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	jobs := recordEditRunSpawns(t, nil)

	got := PostEdit(postPayload("Write", src), greenRun)

	if !strings.Contains(got, "green") {
		t.Fatalf("the fixture's edit is not green: %q", got)
	}
	if len(*jobs) != 1 {
		t.Fatalf("mutation runs started = %d, want 1", len(*jobs))
	}
	j := (*jobs)[0]
	if j.Session != "sess-post" || j.File != src || !sameDir(j.Root, root) {
		t.Errorf("job = %+v, want session sess-post, file %s, root %s", j, src, root)
	}
	if j.Log == "" || j.Done == "" || j.Log == j.Done {
		t.Errorf("job = %+v, want a log and a result file of their own", j)
	}
	if strings.Contains(got, "deferred mutants") {
		t.Errorf("the hook reported a mutation result before any run finished: %q", got)
	}
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestPostEdit_StartsNoMutationRunForATestADocumentOrARedEdit(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget_Ok(t *testing.T) {}\n")
	write(t, root, "notes.md", "# notes\n")
	jobs := recordEditRunSpawns(t, nil)

	PostEdit(postPayload("Write", filepath.Join(root, "widget_test.go")), greenRun)
	PostEdit(postPayload("Write", filepath.Join(root, "notes.md")), greenRun)
	PostEdit(postPayload("Write", src), redRun)

	if len(*jobs) != 0 {
		t.Errorf("mutation runs started for a test file, a document and a red edit: %+v", *jobs)
	}
}

func TestPostEdit_StartsNoMutationRunInARepoThatDeclaresNothing(t *testing.T) {
	_, src := mutantsEditFixture(t, false)
	jobs := recordEditRunSpawns(t, nil)
	PostEdit(postPayload("Write", src), greenRun)
	if len(*jobs) != 0 {
		t.Errorf("mutation runs started in an undeclared repo: %+v", *jobs)
	}
}

// One run at a time per session and tree: an edit made while the last one is
// unfinished starts nothing, and the run's result is not overwritten.
func TestPostEdit_AnUnfinishedRunIsNotStartedAgain(t *testing.T) {
	_, src := mutantsEditFixture(t, true)
	jobs := recordEditRunSpawns(t, nil)
	PostEdit(postPayload("Write", src), greenRun)
	PostEdit(postPayload("Write", src), greenRun)
	PostEdit(postPayload("Write", src), greenRun)
	if len(*jobs) != 1 {
		t.Errorf("mutation runs started = %d over three edits with the first unfinished, want 1", len(*jobs))
	}
}

func TestPostEdit_ReportsAFinishedRunAtTheNextHookAndStartsAnother(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	log := "gate edit: mutants → REJECTED — a mutant of a line this commit adds survived its tests (2.0s, slowest mutant 1.0s)\n" +
		"widget.go:3:41: CONDITIONALS_BOUNDARY\nmutants: 2 tested, 1 caught, 0 unviable, 1 missed (0 accepted), 0 unmeasured\n"
	jobs := recordEditRunSpawns(t, finishWith(t, "refused", log))

	first := PostEdit(postPayload("Write", src), greenRun)
	if strings.Contains(first, "deferred mutants") {
		t.Fatalf("the first hook reported a result it could not have: %q", first)
	}
	second := PostEdit(postPayload("Write", src), greenRun)

	for _, want := range []string{"gate: deferred mutants", "widget.go", "refused", "widget.go:3:41: CONDITIONALS_BOUNDARY"} {
		if !strings.Contains(second, want) {
			t.Errorf("second hook lacks %q:\n%s", want, second)
		}
	}
	if len(*jobs) != 2 {
		t.Errorf("mutation runs started = %d, want the finished one reported and a fresh one started (2)", len(*jobs))
	}
	if !strings.Contains(second, root) {
		t.Errorf("the carried line does not name the tree it belongs to:\n%s", second)
	}
}

func TestPostEdit_AResultIsReportedOnce(t *testing.T) {
	_, src := mutantsEditFixture(t, true)
	// The first run finishes; the later ones stay unfinished so nothing new
	// arrives to be reported.
	runs := 0
	finished := finishWith(t, "ok", "gate edit: mutants → 3 tested, 3 caught, 0 unviable, 0 accepted (1.0s, slowest mutant 0.5s)\n")
	jobs := recordEditRunSpawns(t, func(j mutantsEditJob) {
		if runs++; runs == 1 {
			finished(j)
		}
	})
	PostEdit(postPayload("Write", src), greenRun)
	second := PostEdit(postPayload("Write", src), greenRun)
	third := PostEdit(postPayload("Write", src), greenRun)
	if !strings.Contains(second, "3 tested, 3 caught") {
		t.Errorf("second hook lacks the result:\n%s", second)
	}
	if strings.Contains(third, "deferred mutants") {
		t.Errorf("third hook repeats the result:\n%s", third)
	}
	if len(*jobs) != 2 {
		t.Errorf("mutation runs started = %d, want 2", len(*jobs))
	}
}

// Sessions never read each other's runs.
func TestPostEdit_AnotherSessionsResultIsNotReported(t *testing.T) {
	_, src := mutantsEditFixture(t, true)
	recordEditRunSpawns(t, finishWith(t, "ok", "gate edit: mutants → 1 tested, 1 caught, 0 unviable, 0 accepted (1.0s, slowest mutant 0.5s)\n"))
	PostEdit(postPayload("Write", src), greenRun)
	if got := promptHarvest("another-session"); strings.Contains(got, "deferred mutants") {
		t.Errorf("another session was handed this session's result: %q", got)
	}
	if got := promptHarvest("sess-post"); !strings.Contains(got, "deferred mutants") {
		t.Errorf("the owning session was not handed its result: %q", got)
	}
}

// A run that never finishes is dropped after its limit, saying so, so a dead
// process cannot leave the session waiting or block the next run.
func TestMutantsEditHarvest_ARunPastItsLimitIsReportedNotMeasured(t *testing.T) {
	_, src := mutantsEditFixture(t, true)
	jobs := recordEditRunSpawns(t, nil)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	t.Cleanup(setMutantsEditNowForTest(func() time.Time { return now }))
	PostEdit(postPayload("Write", src), greenRun)
	if len(*jobs) != 1 {
		t.Fatalf("mutation runs started = %d, want 1", len(*jobs))
	}

	now = now.Add(mutantsEditMax)
	if got := harvestMutantsEdit("sess-post"); len(got) != 0 {
		t.Errorf("a run exactly at its limit was reported: %v", got)
	}
	now = now.Add(time.Nanosecond)
	got := harvestMutantsEdit("sess-post")
	if len(got) != 1 || !strings.Contains(got[0], "NOT MEASURED") || !strings.Contains(got[0], mutantsEditMax.String()) {
		t.Errorf("a run past its limit = %v, want one NOT MEASURED line naming the limit", got)
	}
	if again := harvestMutantsEdit("sess-post"); len(again) != 0 {
		t.Errorf("the abandoned run is reported again: %v", again)
	}
	PostEdit(postPayload("Write", src), greenRun)
	if len(*jobs) != 2 {
		t.Errorf("mutation runs started = %d, want a fresh one after the abandoned one was dropped", len(*jobs))
	}
}

// The real spawn never starts from a Go test binary, which would answer the
// verb by running its whole suite, and never reaches the launch.
func TestSpawnMutantsEdit_DeclinesFromATestBinary(t *testing.T) {
	dir := t.TempDir()
	job := mutantsEditJob{Session: "s", Root: dir, File: filepath.Join(dir, "x.go"), Log: filepath.Join(dir, "x.log"), Done: filepath.Join(dir, "x.done")}
	launched := 0
	prev := mutantsEditLaunchFn
	mutantsEditLaunchFn = func(*exec.Cmd, string) (int, bool) { launched++; return 1, true }
	t.Cleanup(func() { mutantsEditLaunchFn = prev })
	if pid, ok := spawnMutantsEdit(job); ok || pid != 0 {
		t.Errorf("spawnMutantsEdit = (%d, %v) from a test binary, want it to decline", pid, ok)
	}
	if launched != 0 {
		t.Errorf("the spawn launched %d command(s) from a test binary", launched)
	}
	if _, err := os.Stat(job.Log); err == nil {
		t.Error("a declined spawn left a log behind")
	}
}

func TestMutantsEditCommand_RunsTheVerbOnTheEditInTheTree(t *testing.T) {
	t.Parallel()
	job := mutantsEditJob{Root: "/repo", File: "/repo/x.go", Done: "/state/x.done"}
	cmd := mutantsEditCommand("/usr/local/bin/aphrollo", job)
	want := []string{"/usr/local/bin/aphrollo", "gate", "mutants", "edit", "--file", "/repo/x.go", "--done", "/state/x.done"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("argv = %v, want %v", cmd.Args, want)
	}
	if cmd.Dir != "/repo" {
		t.Errorf("dir = %q, want /repo", cmd.Dir)
	}
	for _, env := range []string{"CI=1", "NO_COLOR=1"} {
		if !slices.Contains(cmd.Env, env) {
			t.Errorf("env lacks %s", env)
		}
	}
}

// A launch that cannot open its log, or cannot start the command, starts
// nothing and leaves no log to be mistaken for a run's.
func TestLaunchMutantsEdit_WhatCannotStartLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if pid, ok := launchMutantsEdit(exec.Command(filepath.Join(dir, "no-such-binary")), filepath.Join(dir, "missing-dir", "x.log")); ok || pid != 0 {
		t.Errorf("a log that cannot be opened = (%d, %v), want it declined", pid, ok)
	}
	log := filepath.Join(dir, "x.log")
	if pid, ok := launchMutantsEdit(exec.Command(filepath.Join(dir, "no-such-binary")), log); ok || pid != 0 {
		t.Errorf("a command with no binary = (%d, %v), want it declined", pid, ok)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a launch that failed left its log behind")
	}
}

// A run that could not be started leaves no record behind to wait on.
func TestPostEdit_ARunThatCouldNotStartLeavesNothingToWaitOn(t *testing.T) {
	_, src := mutantsEditFixture(t, true)
	calls := 0
	t.Cleanup(SetMutantsEditSpawnForTest(func(mutantsEditJob) (int, bool) { calls++; return 0, false }))
	PostEdit(postPayload("Write", src), greenRun)
	PostEdit(postPayload("Write", src), greenRun)
	if calls != 2 {
		t.Errorf("spawns attempted = %d, want each edit to try again (2)", calls)
	}
}

// Old records of sessions that ended without reading them are swept, at the
// day's edge and not before.
func TestStartMutantsEdit_SweepsRecordsOlderThanADay(t *testing.T) {
	_, src := mutantsEditFixture(t, true)
	recordEditRunSpawns(t, nil)
	dir := mutantsEditDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	t.Cleanup(setMutantsEditNowForTest(func() time.Time { return now }))
	for name, age := range map[string]time.Duration{
		"stale.json": mutantsEditKeep + time.Second,
		"edge.json":  mutantsEditKeep,
		"fresh.json": time.Hour,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	PostEdit(postPayload("Write", src), greenRun)
	for name, want := range map[string]bool{"stale.json": false, "edge.json": true, "fresh.json": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s present = %v, want %v", name, err == nil, want)
		}
	}
}
