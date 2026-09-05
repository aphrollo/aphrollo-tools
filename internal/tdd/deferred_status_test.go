package tdd

import (
	"strings"
	"testing"
	"time"
)

// TestActiveDeferredJobs_ListsOnlyJobsWithoutAResult pins `gate status`'s
// first source: a job the wrapper already finished (result file present) is
// not "active" any more, whether or not the next hook has harvested it yet.
func TestActiveDeferredJobs_ListsOnlyJobsWithoutAResult(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	running := t.TempDir()
	finished := t.TempDir()

	saveDeferredJob(DeferredJob{Project: running, Phase: "build", PID: 111, Started: time.Now(), Session: "s1"})
	saveDeferredJob(DeferredJob{Project: finished, Phase: "run", PID: 222, Started: time.Now(), Session: "s2"})
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
