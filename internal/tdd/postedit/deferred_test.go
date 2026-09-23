package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDeferredJob_RoundTripsPerProject pins the record the whole feature
// rests on: a build that outlived its hook is described on disk, keyed by
// PROJECT (not by session), so the next hook — in ANY session — can find it
// again. Sessions come and go; the build outlives them.
func TestDeferredJob_RoundTripsPerProject(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a, b := t.TempDir(), t.TempDir()

	job := DeferredJob{
		Project: a, Phase: "build", Runner: []string{"cargo", "nextest", "run", "-p", "server", "--no-run"},
		Dir: a, PID: 4242, Started: time.Now().UTC().Truncate(time.Second),
		HeadSHA: "abc123", FileHash: "deadbeef", Session: "s1",
	}
	saveDeferredJob(job)

	got, ok := loadDeferredJob("s1", a)
	if !ok {
		t.Fatal("a saved job must be findable again by its project")
	}
	if got.PID != 4242 || got.HeadSHA != "abc123" || got.FileHash != "deadbeef" || got.Phase != "build" {
		t.Fatalf("job round-tripped as %+v, want the recorded identity back", got)
	}
	if _, ok := loadDeferredJob("s1", b); ok {
		t.Fatal("another project must not see this job")
	}

	clearDeferredJob("s1", a)
	if _, ok := loadDeferredJob("s1", a); ok {
		t.Fatal("a cleared job must be gone")
	}
}

// TestDeferredJob_FinishedWhenTheWrapperWroteItsResult pins how liveness is
// decided: the detached phase is wrapped by `aphrollo gate runphase`, which
// writes a result file when it is done. No PID probing — a PID can be reused,
// and on Windows it cannot be signalled portably anyway.
func TestDeferredJob_FinishedWhenTheWrapperWroteItsResult(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	job := DeferredJob{Project: root, Phase: "build", Started: time.Now()}
	saveDeferredJob(job)
	job, _ = loadDeferredJob("", root)

	if _, done := deferredResult(job); done {
		t.Fatal("a job whose wrapper has not written a result is still running")
	}
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 0, Seconds: 12.5})
	res, done := deferredResult(job)
	if !done {
		t.Fatal("once the result file exists the job is finished")
	}
	if res.ExitCode != 0 || res.Seconds != 12.5 {
		t.Fatalf("result = %+v, want the wrapper's own numbers", res)
	}
}

// TestDeferredJob_DirtyMarkSurvivesTheReload pins the no-kill policy's
// mechanism: a new edit to the SAME project while a build runs never kills
// it (cargo is doing useful work); it marks the job dirty so the harvest
// knows the source moved on and must rebuild for the latest content.
func TestDeferredJob_DirtyMarkSurvivesTheReload(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", FileHash: "old"})

	markDeferredDirty("", root, "new-hash")

	got, ok := loadDeferredJob("", root)
	if !ok {
		t.Fatal("marking dirty must never drop the job")
	}
	if !got.Dirty {
		t.Fatal("the job must be marked dirty")
	}
	if got.FileHash != "new-hash" {
		t.Fatalf("FileHash = %q, want the latest edit's hash %q", got.FileHash, "new-hash")
	}
}

// TestUpdateDeferredJob_ConcurrentWritersNeverLoseAnUpdate pins issue #294:
// the detached runphase process (stampDeferredStart) and a later PostToolUse
// hook (markDeferredDirty) write the SAME job record from two OS processes.
// A plain load-mutate-save on both sides drops whichever wrote first. Every
// goroutine here increments the same field by one; if any update is lost the
// final count comes up short.
func TestUpdateDeferredJob_ConcurrentWritersNeverLoseAnUpdate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root})

	const writers = 50
	var wg sync.WaitGroup
	wg.Add(writers)
	for range writers {
		go func() {
			defer wg.Done()
			updateDeferredJob("", root, func(j *DeferredJob) {
				j.PID++
			})
		}()
	}
	wg.Wait()

	got, ok := loadDeferredJob("", root)
	if !ok {
		t.Fatal("the job must survive concurrent updates")
	}
	if got.PID != writers {
		t.Fatalf("PID = %d after %d concurrent read-modify-write increments, want %d — an update was lost to a race", got.PID, writers, writers)
	}
}

// TestDeferredExpired_OnlyBeyondTheMaximum pins the ONE case where a healthy
// build is killed: it has been running longer than any real build should.
// Everything shorter is left alone — killing a warm build to start the same
// build again is pure loss.
func TestDeferredExpired_OnlyBeyondTheMaximum(t *testing.T) {
	t.Setenv("APHROLLO_DEFERRED_MAX_SECS", "")
	now := time.Now()
	fresh := DeferredJob{Started: now.Add(-5 * time.Minute)}
	old := DeferredJob{Started: now.Add(-11 * time.Minute)}
	if deferredExpired(fresh, now) {
		t.Error("a five-minute-old build is healthy — a Bevy-sized crate takes that")
	}
	if !deferredExpired(old, now) {
		t.Error("past the maximum (default 600s) a job is abandoned, not waited for")
	}

	t.Setenv("APHROLLO_DEFERRED_MAX_SECS", "60")
	if !deferredExpired(DeferredJob{Started: now.Add(-2 * time.Minute)}, now) {
		t.Error("APHROLLO_DEFERRED_MAX_SECS must be honoured")
	}
}

// TestDeferredHarvest_OnlyWhenTheSourceStillMatches pins the orphan rule: a
// job left behind by a session that ended is harvestable by any later hook —
// but only if it was built from the SAME commit and the SAME file content.
// Otherwise its result describes code that no longer exists.
func TestDeferredHarvest_OnlyWhenTheSourceStillMatches(t *testing.T) {
	job := DeferredJob{HeadSHA: "abc", FileHash: "111"}
	if !deferredMatchesSource(job, "abc", "111") {
		t.Error("same commit and same content: the result is about this code")
	}
	if deferredMatchesSource(job, "def", "111") {
		t.Error("a different HEAD means the result describes other code")
	}
	if deferredMatchesSource(job, "abc", "222") {
		t.Error("a different file hash means the edit moved on")
	}
	if deferredMatchesSource(DeferredJob{Dirty: true, HeadSHA: "abc", FileHash: "111"}, "abc", "111") {
		t.Error("a job marked dirty is known to be stale whatever the hashes say")
	}
}

// TestDeferredLogAndResultPaths_LiveUnderTheStateDir pins where the artifacts
// go: beside the gate's other state, one set per project, never in the repo.
func TestDeferredLogAndResultPaths_LiveUnderTheStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", state)
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Phase: "build"})
	job, _ := loadDeferredJob("", root)

	for name, path := range map[string]string{"log": job.Log, "result": job.Result} {
		if path == "" {
			t.Fatalf("the job must name its %s file", name)
		}
		if !strings.HasPrefix(filepath.Clean(path), filepath.Join(state, "gate-state", "deferred")) {
			t.Fatalf("%s path = %q, want it under the state dir's deferred/", name, path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(job.Log), 0o700); err != nil {
		t.Fatal(err)
	}
}
