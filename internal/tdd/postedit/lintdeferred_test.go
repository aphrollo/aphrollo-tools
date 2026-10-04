package postedit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/lock"
)

// The edit hook starts the linter's whole configured set over the edited
// package, detached, and the next hook of the session reports what it found
// as a `gate: deferred golangci-lint` line. The inline lint keeps the fast
// set. The spawn is a seam: no test here starts the real detached process.

// recordLintSpawns answers the runs the hook asked to start. finish, when set,
// plays the detached process: it writes the log and the result at once.
func recordLintSpawns(t *testing.T, finish func(j lintEditJob)) *[]lintEditJob {
	t.Helper()
	var jobs []lintEditJob
	prev := lintEditSpawnFn
	t.Cleanup(func() { lintEditSpawnFn = prev })
	lintEditSpawnFn = func(j lintEditJob) (int, bool) {
		jobs = append(jobs, j)
		if finish != nil {
			finish(j)
		}
		return 4242, true
	}
	return &jobs
}

// lintOutcome is what the detached run leaves: its log and its result.
func lintOutcome(t *testing.T, log string, out PhaseOutcome) func(j lintEditJob) {
	t.Helper()
	return func(j lintEditJob) {
		if err := os.WriteFile(j.Log, []byte(log), 0o600); err != nil {
			t.Fatal(err)
		}
		writePhaseResult(j.Result, out)
	}
}

func TestStartLintEdit_StartsTheFullConfiguredSetOverTheEditedPackage(t *testing.T) {
	root, src := editedWidget(t)
	lintSeams(t, "", false)
	jobs := recordLintSpawns(t, nil)

	startLintEdit("sess", src, nil)

	if len(*jobs) != 1 {
		t.Fatalf("lint runs started = %d, want 1", len(*jobs))
	}
	j := (*jobs)[0]
	data, err := os.ReadFile(j.Job)
	if err != nil {
		t.Fatalf("the phase job file was not written: %v", err)
	}
	var phase DeferredJob
	if err := json.Unmarshal(data, &phase); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(phase.Runner, " ")
	if strings.Contains(joined, "--fast-only") {
		t.Errorf("the deferred run %q narrows to the fast set", joined)
	}
	patch := ""
	for _, a := range phase.Runner {
		if p, ok := strings.CutPrefix(a, "--new-from-patch="); ok {
			patch = p
		}
	}
	if phase.Runner[0] != "golangci-lint" || phase.Runner[1] != "run" || patch == "" {
		t.Errorf("runner = %q, want golangci-lint run --new-from-patch=<file> ...", joined)
	}
	if body, err := os.ReadFile(patch); err != nil || !strings.Contains(string(body), "widget.go") {
		t.Errorf("patch file = %q, err %v, want the edit's diff of widget.go", body, err)
	}
	if last := phase.Runner[len(phase.Runner)-1]; last != "." {
		t.Errorf("a file at the repo top lints package %q, want \".\"", last)
	}
	if phase.Dir != root || phase.Log != j.Log || phase.Result != j.Result {
		t.Errorf("phase dir %q log %q result %q, want root %q log %q result %q", phase.Dir, phase.Log, phase.Result, root, j.Log, j.Result)
	}
	// The run must not be able to touch the suite's own deferred record.
	if phase.Session == "sess" || phase.Session == "" {
		t.Errorf("phase session = %q, want one that no suite job is recorded under", phase.Session)
	}
	if _, err := os.Stat(lintDeferredStem(lintDeferredDir(), "sess", root) + ".json"); err != nil {
		t.Errorf("no record left for the harvest: %v", err)
	}
}

func TestStartLintEdit_LintsTheEditedFilesOwnPackage(t *testing.T) {
	root, _ := editedWidget(t)
	mustWrite(t, filepath.Join(root, "sub", "dir", "part.go"), "package dir\n\nfunc Part() int { return 1 }\n")
	lintSeams(t, "", false)
	jobs := recordLintSpawns(t, nil)

	startLintEdit("sess", filepath.Join(root, "sub", "dir", "part.go"), nil)

	if len(*jobs) != 1 {
		t.Fatalf("lint runs started = %d, want 1", len(*jobs))
	}
	data, _ := os.ReadFile((*jobs)[0].Job)
	var phase DeferredJob
	if err := json.Unmarshal(data, &phase); err != nil {
		t.Fatal(err)
	}
	if last := phase.Runner[len(phase.Runner)-1]; last != "./sub/dir" {
		t.Errorf("package = %q, want ./sub/dir", last)
	}
}

func TestStartLintEdit_StandsDown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session string
		target  func(t *testing.T, root string) string
		setup   func(t *testing.T, root string)
	}{
		{name: "no session", session: ""},
		{name: "a file that is not Go", session: "s", target: func(t *testing.T, root string) string {
			mustWrite(t, filepath.Join(root, "notes.md"), "words\n")
			return filepath.Join(root, "notes.md")
		}},
		{name: "a file identical to HEAD", session: "s", setup: func(t *testing.T, root string) {
			mustWrite(t, filepath.Join(root, "widget.go"), "package m\n\nfunc Size() int { return 1 }\n")
		}},
		{name: "the linter absent", session: "s", setup: func(t *testing.T, root string) { lintEditLook = func() bool { return false } }},
		{name: "the box loaded at exactly twice its cores", session: "s", setup: func(t *testing.T, root string) {
			lintEditLoad = func() (float64, int, bool) { return 16, 8, true }
		}},
		{name: "another lint holding the lock", session: "s", setup: func(t *testing.T, root string) {
			release, ok := lock.TryAcquireLintLock("held", root)
			if !ok {
				t.Fatal("setup: the lint lock was already held")
			}
			t.Cleanup(release)
		}},
		{name: "a backoff after a timeout", session: "s", setup: func(t *testing.T, root string) { markLintBackoff(root) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, src := editedWidget(t)
			lintSeams(t, "", false)
			jobs := recordLintSpawns(t, nil)
			if tc.setup != nil {
				tc.setup(t, root)
			}
			target := src
			if tc.target != nil {
				target = tc.target(t, root)
			}
			startLintEdit(tc.session, target, nil)
			if len(*jobs) != 0 {
				t.Fatalf("lint runs started = %d, want none", len(*jobs))
			}
		})
	}
}

// A box just under the load line still starts the run, so the line above is
// the boundary and not a wider band.
func TestStartLintEdit_ABoxJustUnderTheLoadLineStartsTheRun(t *testing.T) {
	_, src := editedWidget(t)
	lintSeams(t, "", false)
	lintEditLoad = func() (float64, int, bool) { return 15.99, 8, true }
	jobs := recordLintSpawns(t, nil)
	startLintEdit("s", src, nil)
	if len(*jobs) != 1 {
		t.Fatalf("lint runs started = %d, want 1 under the load line", len(*jobs))
	}
}

// One run per session and tree: an edit made while the last run is unfinished
// or unread starts nothing.
func TestStartLintEdit_AnUnreadRecordHoldsTheNextRunOff(t *testing.T) {
	_, src := editedWidget(t)
	lintSeams(t, "", false)
	jobs := recordLintSpawns(t, nil)
	startLintEdit("s", src, nil)
	startLintEdit("s", src, nil)
	if len(*jobs) != 1 {
		t.Fatalf("lint runs started = %d, want 1", len(*jobs))
	}
	startLintEdit("other", src, nil)
	if len(*jobs) != 2 {
		t.Fatalf("a second session started %d runs in total, want 2", len(*jobs))
	}
}

// A run that could not be started leaves no record to wait on.
func TestStartLintEdit_ARunThatCouldNotStartLeavesNothingToWaitOn(t *testing.T) {
	root, src := editedWidget(t)
	lintSeams(t, "", false)
	calls := 0
	prev := lintEditSpawnFn
	t.Cleanup(func() { lintEditSpawnFn = prev })
	lintEditSpawnFn = func(lintEditJob) (int, bool) { calls++; return 0, false }
	startLintEdit("s", src, nil)
	startLintEdit("s", src, nil)
	if calls != 2 {
		t.Errorf("spawns attempted = %d, want each edit to try again (2)", calls)
	}
	if _, err := os.Stat(lintDeferredStem(lintDeferredDir(), "s", root) + ".json"); err == nil {
		t.Error("a run that never started left its record behind")
	}
}

func TestStartLintEdit_SweepsFilesOlderThanADayAtTheDaysEdge(t *testing.T) {
	_, src := editedWidget(t)
	lintSeams(t, "", false)
	recordLintSpawns(t, nil)
	dir := lintDeferredDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old, edge := filepath.Join(dir, "old.log"), filepath.Join(dir, "edge.log")
	for _, p := range []string{old, edge} {
		mustWrite(t, p, "x")
	}
	restore := setLintDeferredNowForTest(func() time.Time { return now })
	defer restore()
	if err := os.Chtimes(old, now, now.Add(-lintDeferredKeep-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(edge, now, now.Add(-lintDeferredKeep)); err != nil {
		t.Fatal(err)
	}
	startLintEdit("s", src, nil)
	if _, err := os.Stat(old); err == nil {
		t.Error("a file older than a day survived the sweep")
	}
	if _, err := os.Stat(edge); err != nil {
		t.Errorf("a file exactly a day old was swept: %v", err)
	}
}

// startedLint runs one edit through startLintEdit with the detached run
// playing finish, and answers the file, the tree and the run.
func startedLint(t *testing.T, known []string, finish func(j lintEditJob)) (root, src string, job lintEditJob) {
	t.Helper()
	root, src = editedWidget(t)
	lintSeams(t, "", false)
	jobs := recordLintSpawns(t, finish)
	startLintEdit("sess", src, known)
	if len(*jobs) != 1 {
		t.Fatalf("lint runs started = %d, want 1", len(*jobs))
	}
	return root, src, (*jobs)[0]
}

func TestHarvestLintEdit_ReportsAFinishedRunsFindingsOnceAndClearsIt(t *testing.T) {
	log := "widget.go:3:22: SA4006: value of x is never used (staticcheck)\nother.go:9:1: unused thing (unused)\n"
	root, _, _ := startedLint(t, nil, lintOutcome(t, log, PhaseOutcome{ExitCode: 1, Seconds: 1.5}))

	got := harvestLintEdit("sess")

	if len(got) != 1 {
		t.Fatalf("harvest = %q, want one line", got)
	}
	want := "gate: deferred golangci-lint in " + root + " (widget.go) → 1 finding in 1.5s: widget.go:3:22: SA4006: value of x is never used (staticcheck)"
	if got[0] != want {
		t.Errorf("line = %q, want %q", got[0], want)
	}
	if again := harvestLintEdit("sess"); len(again) != 0 {
		t.Errorf("a second harvest reported %q, want nothing", again)
	}
	entries, _ := os.ReadDir(lintDeferredDir())
	if len(entries) != 0 {
		t.Errorf("a reported run left %d file(s) behind", len(entries))
	}
}

func TestHarvestLintEdit_ANameOfTwoFindingsIsPlural(t *testing.T) {
	log := "widget.go:3:1: one (x)\nwidget.go:4:1: two (x)\n"
	startedLint(t, nil, lintOutcome(t, log, PhaseOutcome{ExitCode: 1, Seconds: 2}))
	got := harvestLintEdit("sess")
	if len(got) != 1 || !strings.Contains(got[0], "→ 2 findings in 2.0s: widget.go:3:1: one (x); widget.go:4:1: two (x)") {
		t.Fatalf("harvest = %q, want two findings named", got)
	}
}

func TestHarvestLintEdit_NamesFiveFindingsAndCountsTheRest(t *testing.T) {
	var log strings.Builder
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&log, "widget.go:%d:1: finding %d (x)\n", i, i)
	}
	startedLint(t, nil, lintOutcome(t, log.String(), PhaseOutcome{ExitCode: 1}))
	got := harvestLintEdit("sess")
	if len(got) != 1 || !strings.Contains(got[0], "finding 5 (x)") || strings.Contains(got[0], "finding 6 (x)") || !strings.HasSuffix(got[0], " and 1 more") {
		t.Fatalf("harvest = %q, want five findings and \"and 1 more\"", got)
	}
}

// A finding the inline fast run already put on the edit's gate line is not
// said again; one it did not is.
func TestHarvestLintEdit_DropsWhatTheInlineRunAlreadyReported(t *testing.T) {
	known := "widget.go:3:22: ineffectual assignment to x (ineffassign)"
	log := known + "\nwidget.go:4:1: SA4006: y is never used (staticcheck)\n"
	startedLint(t, []string{known}, lintOutcome(t, log, PhaseOutcome{ExitCode: 1}))
	got := harvestLintEdit("sess")
	if len(got) != 1 || strings.Contains(got[0], "ineffassign") || !strings.Contains(got[0], "→ 1 finding in") || !strings.Contains(got[0], "SA4006") {
		t.Fatalf("harvest = %q, want only the staticcheck finding", got)
	}
	startedLint(t, []string{known}, lintOutcome(t, known+"\n", PhaseOutcome{ExitCode: 1}))
	if got := harvestLintEdit("sess"); len(got) != 0 {
		t.Fatalf("a run whose findings were all reported inline said %q, want nothing", got)
	}
}

// Nothing to say: a clean run, a linter that could not analyse the package
// (a mid-edit compile error is the suite's finding), contention with another
// lint, and a run that stood down for want of a slot or memory.
func TestHarvestLintEdit_SaysNothingWhenThereIsNothingToReport(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  string
		out  PhaseOutcome
	}{
		{"a clean run", "", PhaseOutcome{ExitCode: 0}},
		{"exit 1 with only another file's findings", "other.go:9:1: unused thing (unused)\n", PhaseOutcome{ExitCode: 1}},
		{"a run that could not analyse the package", "widget.go:3:1: could not import x\n", PhaseOutcome{ExitCode: 3}},
		{"contention with another lint", "Error: parallel golangci-lint is running\nwidget.go:3:1: a (b)\n", PhaseOutcome{ExitCode: 3}},
		{"no slot came free", "aphrollo: no build slot came free\n", PhaseOutcome{ExitCode: 125, SetupFailed: true}},
		{"no memory to start", "aphrollo: SKIPPED\n", PhaseOutcome{ExitCode: 125, Inconclusive: "SKIPPED — no memory"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startedLint(t, nil, lintOutcome(t, tc.log, tc.out))
			if got := harvestLintEdit("sess"); len(got) != 0 {
				t.Fatalf("harvest = %q, want nothing", got)
			}
			if entries, _ := os.ReadDir(lintDeferredDir()); len(entries) != 0 {
				t.Errorf("the harvest left %d file(s) behind", len(entries))
			}
		})
	}
}

// A result about a file that changed since is said so, never passed off as
// current.
func TestHarvestLintEdit_LabelsAResultForAnEarlierVersionOfTheFile(t *testing.T) {
	_, src, _ := startedLint(t, nil, lintOutcome(t, "widget.go:3:1: a (b)\n", PhaseOutcome{ExitCode: 1}))
	mustWrite(t, src, "package m\n\nfunc Size() int { return 3 }\n")
	got := harvestLintEdit("sess")
	if len(got) != 1 || !strings.Contains(got[0], "the file changed since this run started") {
		t.Fatalf("harvest = %q, want the stale label", got)
	}
}

func TestHarvestLintEdit_ARunStillGoingIsLeftAloneUntilExactlyItsLimit(t *testing.T) {
	root, _, job := startedLint(t, nil, nil)
	killed := 0
	prevKill := killDeferredFn
	killDeferredFn = func(j DeferredJob) {
		killed++
		if j.PID != 4242 {
			t.Errorf("killed pid %d, want 4242", j.PID)
		}
	}
	t.Cleanup(func() { killDeferredFn = prevKill })
	record, err := os.ReadFile(lintDeferredStem(lintDeferredDir(), "sess", root) + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var saved lintEditJob
	if err := json.Unmarshal(record, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.PID != 4242 || saved.Job != job.Job {
		t.Fatalf("record pid %d job %q, want 4242 and %q", saved.PID, saved.Job, job.Job)
	}

	restore := setLintDeferredNowForTest(func() time.Time { return saved.Started.Add(lintDeferredMax) })
	if got := harvestLintEdit("sess"); len(got) != 0 || killed != 0 {
		t.Fatalf("a run exactly at its limit: harvest %q, %d kills; want neither", got, killed)
	}
	restore()
	restore = setLintDeferredNowForTest(func() time.Time { return saved.Started.Add(lintDeferredMax + time.Nanosecond) })
	defer restore()
	got := harvestLintEdit("sess")
	if len(got) != 1 || !strings.Contains(got[0], "NOT LINTED") || !strings.Contains(got[0], lintDeferredMax.String()) || killed != 1 {
		t.Fatalf("a run past its limit: harvest %q, %d kills; want one NOT LINTED line and one kill", got, killed)
	}
	if again := harvestLintEdit("sess"); len(again) != 0 {
		t.Errorf("an abandoned run was reported twice: %q", again)
	}
}

// A run whose spawn reported no pid has no process to kill; the abandonment is
// still reported.
func TestHarvestLintEdit_AnAbandonedRunWithNoPidKillsNothing(t *testing.T) {
	_, src := editedWidget(t)
	lintSeams(t, "", false)
	prevSpawn := lintEditSpawnFn
	lintEditSpawnFn = func(lintEditJob) (int, bool) { return 0, true }
	prevKill := killDeferredFn
	killed := 0
	killDeferredFn = func(DeferredJob) { killed++ }
	t.Cleanup(func() { lintEditSpawnFn, killDeferredFn = prevSpawn, prevKill })
	startLintEdit("sess", src, nil)
	record, err := os.ReadFile(lintDeferredStem(lintDeferredDir(), "sess", repoRootNear(filepath.Dir(src))) + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var saved lintEditJob
	if err := json.Unmarshal(record, &saved); err != nil {
		t.Fatal(err)
	}
	defer setLintDeferredNowForTest(func() time.Time { return saved.Started.Add(lintDeferredMax + time.Nanosecond) })()

	got := harvestLintEdit("sess")

	if len(got) != 1 || !strings.Contains(got[0], "NOT LINTED") || killed != 0 {
		t.Fatalf("harvest %q, %d kills; want one NOT LINTED line and no kill", got, killed)
	}
}

func TestHarvestLintEdit_OtherSessionsAndNoStateReportNothing(t *testing.T) {
	startedLint(t, nil, lintOutcome(t, "widget.go:3:1: a (b)\n", PhaseOutcome{ExitCode: 1}))
	if got := harvestLintEdit("someone-else"); len(got) != 0 {
		t.Errorf("another session was told %q", got)
	}
	if got := harvestLintEdit(""); len(got) != 0 {
		t.Errorf("no session was told %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	if got := harvestLintEdit("sess"); len(got) != 0 {
		t.Errorf("no state directory: %q", got)
	}
}

// The whole path through the hook: an edit starts the run, and the next hook
// of the session carries its findings on their own deferred line.
func TestPostEdit_DeliversTheDeferredLintAtTheNextHook(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, src := editedWidget(t)
	lintSeams(t, "", false)
	recordLintSpawns(t, lintOutcome(t, "widget.go:3:22: SA4006: x is never used (staticcheck)\n", PhaseOutcome{ExitCode: 1, Seconds: 0.5}))

	first := PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))
	startLintEdit("sess-post", src, nil)
	if strings.Contains(first, "gate: deferred golangci-lint") {
		t.Fatalf("the run's result arrived in the hook that started it: %q", first)
	}
	second := PostEdit(postPayload("Edit", src), fakeRun(true, "ok\nPASS"))
	want := "gate: deferred golangci-lint in " + root + " (widget.go) → 1 finding in 0.5s"
	if !strings.Contains(second, want) {
		t.Fatalf("the next hook = %q, want it to carry %q", second, want)
	}
}

// ratchet: test_removed TestPostEdit_TheDeferredRunKnowsWhatTheInlineRunReported: the edit hook starts no deferred lint run, so there is no hand-off of the inline findings

// The real spawn never starts from a Go test binary, which would answer the
// verb by running its whole suite, and never reaches the launch.
func TestSpawnLintEdit_DeclinesFromATestBinary(t *testing.T) {
	dir := t.TempDir()
	job := lintEditJob{Session: "s", Root: dir, Job: filepath.Join(dir, "x.job.json"), Log: filepath.Join(dir, "x.log")}
	launched := 0
	prev := lintEditLaunchFn
	lintEditLaunchFn = func(*exec.Cmd) (int, bool) { launched++; return 1, true }
	t.Cleanup(func() { lintEditLaunchFn = prev })
	if pid, ok := spawnLintEdit(job); ok || pid != 0 {
		t.Errorf("spawnLintEdit = (%d, %v) from a test binary, want it to decline", pid, ok)
	}
	if launched != 0 {
		t.Errorf("the spawn launched %d command(s) from a test binary", launched)
	}
}

func TestLintEditCommand_RunsTheRunphaseWrapperOnTheJobFileInTheTree(t *testing.T) {
	t.Parallel()
	cmd := lintEditCommand("/usr/local/bin/aphrollo", lintEditJob{Root: "/repo", Job: "/state/x.job.json"})
	want := []string{"/usr/local/bin/aphrollo", "gate", "runphase", "--job", "/state/x.job.json"}
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

func TestLaunchLintEdit_WhatCannotStartIsDeclined(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if pid, ok := launchLintEdit(exec.Command(filepath.Join(dir, "no-such-binary"))); ok || pid != 0 {
		t.Errorf("a command with no binary = (%d, %v), want it declined", pid, ok)
	}
}
