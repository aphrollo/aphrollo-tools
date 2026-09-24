package postedit

import (
	"strings"
	"testing"
	"time"
)

// TestActiveDeferredJobs_ListsOnlyJobsWithoutAResult pins `gate status`'s
// first source: a job the wrapper already finished (result file present) is
// not "active" any more, whether or not the next hook has harvested it yet.
// Both jobs carry a pid confirmed live (processStartTimeFn stubbed to agree
// with PIDCreatedAt) so this test isolates the result-based filter from the
// liveness filter added for issue #451, covered separately below.
func TestActiveDeferredJobs_ListsOnlyJobsWithoutAResult(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	created := time.Now()
	prev := processStartTimeFn
	processStartTimeFn = func(pid int) (time.Time, bool) { return created, true }
	t.Cleanup(func() { processStartTimeFn = prev })

	running := t.TempDir()
	finished := t.TempDir()

	saveDeferredJob(DeferredJob{Project: running, Phase: "build", PID: 111, PIDCreatedAt: created, Started: time.Now(), Session: "s1"})
	saveDeferredJob(DeferredJob{Project: finished, Phase: "run", PID: 222, PIDCreatedAt: created, Started: time.Now(), Session: "s2"})
	loaded, ok := loadDeferredJob("s2", finished)
	if !ok {
		t.Fatal("setup: expected the finished job to load back")
	}
	writePhaseResult(loaded.Result, PhaseOutcome{ExitCode: 0})

	active := ActiveDeferredJobs()
	if len(active) != 1 {
		t.Fatalf("expected exactly one active job, got %d: %+v", len(active), active)
	}
	if active[0].Project != running {
		t.Errorf("active job project = %q, want the one with no result yet (%q)", active[0].Project, running)
	}
}

// TestActiveDeferredJobs_ExcludesJobWithDeadPID pins issue #451: a job whose
// pid cannot be confirmed alive (processStartTimeFn fails, matching what
// happens when the OS has no such process) must never render as "running",
// however old or fresh the record.
func TestActiveDeferredJobs_ExcludesJobWithDeadPID(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	prev := processStartTimeFn
	processStartTimeFn = func(pid int) (time.Time, bool) { return time.Time{}, false }
	t.Cleanup(func() { processStartTimeFn = prev })

	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", PID: 999999, PIDCreatedAt: time.Now(), Started: time.Now(), Session: "s1"})

	if active := ActiveDeferredJobs(); len(active) != 0 {
		t.Fatalf("expected a job with a dead pid to be excluded, got %d: %+v", len(active), active)
	}
}

// TestActiveDeferredJobs_ExcludesJobWithNoStartToken pins the other half of
// issue #451: a live pid is not enough on its own. Without PIDCreatedAt there
// is nothing to check the pid's IDENTITY against, and pid reuse over a
// multi-hour window is exactly the case this issue was filed over — so an
// unverifiable record must read as "not confirmed running", never as
// "running" by default.
func TestActiveDeferredJobs_ExcludesJobWithNoStartToken(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	prev := processStartTimeFn
	processStartTimeFn = func(pid int) (time.Time, bool) { return time.Now(), true }
	t.Cleanup(func() { processStartTimeFn = prev })

	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", PID: 111, Started: time.Now(), Session: "s1"})

	if active := ActiveDeferredJobs(); len(active) != 0 {
		t.Fatalf("expected a job with no start token to be excluded, got %d: %+v", len(active), active)
	}
}

// TestFindDeferredJobForProject_MatchesByPathNotSession pins why `gate
// status` can find a hook's job at all: a plain CLI invocation has no
// session id of its own, so the lookup has to go by project path alone.
func TestFindDeferredJobForProject_MatchesByPathNotSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", PID: 7, Started: time.Now(), Session: "some-hook-session"})

	j, ok := findDeferredJobForProject(root)
	if !ok {
		t.Fatal("expected to find the job by project path alone, without knowing its session")
	}
	if j.PID != 7 {
		t.Errorf("found job PID = %d, want 7", j.PID)
	}
}

// TestWaitDeferredEditJob_NothingRecordedReturnsNotOK: a project with no
// deferred job must say so plainly rather than fabricate a verdict.
func TestWaitDeferredEditJob_NothingRecordedReturnsNotOK(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if _, ok := WaitDeferredEditJob(t.TempDir()); ok {
		t.Fatal("a project with no deferred job must report ok=false")
	}
}

// TestWaitDeferredEditJob_DeadPidReturnsNotOKWithoutBlocking pins issue
// #451's `--wait` consequence: a record with no result whose pid cannot be
// confirmed alive must be reported as nothing-to-wait-for immediately, not
// polled every waitDeferredPollInterval out to the deferral ceiling (which,
// for the multi-hour-old records the issue reports, would be indistinguishable
// from hanging forever to a caller with a shell timeout shorter than that).
func TestWaitDeferredEditJob_DeadPidReturnsNotOKWithoutBlocking(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	prev := processStartTimeFn
	processStartTimeFn = func(pid int) (time.Time, bool) { return time.Time{}, false }
	t.Cleanup(func() { processStartTimeFn = prev })

	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", PID: 999999, PIDCreatedAt: time.Now(), Started: time.Now(), Session: "s1"})

	start := time.Now()
	_, ok := WaitDeferredEditJob(root)
	if ok {
		t.Fatal("a job whose pid cannot be confirmed alive must report ok=false, not fabricate a verdict")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("must return immediately for a dead-pid job rather than poll toward the deferral ceiling; took %s", elapsed)
	}
}

// `gate status --wait` compared a result against the source identity the
// job itself recorded, so it could never find the tree had moved on, and it
// printed a result from an earlier tree state as the current verdict. It
// compares against the tree as it stands and labels a moved-past result.
func TestWaitDeferredEditJob_LabelsAResultTheTreeHasMovedPastAsStale(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	RecordFinishedDeferredJobForTest(root, "s1")
	write(t, root, "moved.go", "package m\n")

	advisory, ok := WaitDeferredEditJob(root)

	if !ok {
		t.Fatal("expected the finished result to be reported")
	}
	if !strings.Contains(advisory, "measured on an earlier tree state") || !strings.Contains(advisory, "not a verdict on the current code") {
		t.Fatalf("advisory = %q, want a moved-past result labelled as not about the current code", advisory)
	}
}

// TestWaitDeferredEditJob_ReturnsTheHarvestVerdictOnceDone is the acceptance
// case from issue #430: once a job's result already exists on disk, `gate
// status --wait` returns the SAME verdict line a hook harvesting it would —
// literally the same call, harvestDeferred, that a hook makes.
func TestWaitDeferredEditJob_ReturnsTheHarvestVerdictOnceDone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir() // not a git repo -- headSHAFor(root) resolves to ""
	job := DeferredJob{
		Project: root, Phase: "run", Dir: root, PID: 99,
		Runner: []string{"go", "test", "./..."}, Started: time.Now(),
		HeadSHA: "", FileHash: "hash1", Session: "s1",
	}
	saveDeferredJob(job)
	loaded, ok := loadDeferredJob("s1", root)
	if !ok {
		t.Fatal("setup: expected the job to load back")
	}
	writePhaseResult(loaded.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	advisory, ok := WaitDeferredEditJob(root)
	if !ok {
		t.Fatal("expected a verdict for a job whose result already exists")
	}
	if !strings.Contains(advisory, "gate:") {
		t.Errorf("advisory = %q, want a gate: line", advisory)
	}
}
