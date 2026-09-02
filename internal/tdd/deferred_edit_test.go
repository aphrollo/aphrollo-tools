package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePhases replaces the detached-phase spawner for a test: each spawn is
// recorded, and finishes immediately with the queued outcome (or never, when
// the queue says so). Returns the recorded spawns.
func fakePhases(t *testing.T, outcomes ...*PhaseOutcome) *[]DeferredJob {
	t.Helper()
	var spawned []DeferredJob
	i := 0
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		j.PID = 1000 + i
		j.Started = time.Now()
		saveDeferredJob(j)
		j, _ = loadDeferredJob(j.Project)
		spawned = append(spawned, j)
		var out *PhaseOutcome
		if i < len(outcomes) {
			out = outcomes[i]
		}
		i++
		if out != nil {
			if err := os.WriteFile(j.Log, []byte("test result: ok. 1 passed; 0 failed"), 0o600); err != nil {
				t.Error(err)
			}
			writePhaseResult(j.Result, *out)
		}
		return j, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })
	return &spawned
}

// TestPostEdit_BuildAndRunAreSeparatePhases pins the split the whole feature
// rests on: the edit's tests are BUILT first (`--no-run`) and only then RUN,
// so the budget question ("is this still going?") can be answered about the
// build without discarding it.
func TestPostEdit_BuildAndRunAreSeparatePhases(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	done := &PhaseOutcome{ExitCode: 0, Seconds: 1}
	spawned := fakePhases(t, done, done)

	PostEdit(postPayload("Edit", root+"/src/widget.rs"), fakeRun(true, "ok"))

	if len(*spawned) != 2 {
		t.Fatalf("want a build phase then a run phase, got %d: %+v", len(*spawned), *spawned)
	}
	build, runPhase := (*spawned)[0], (*spawned)[1]
	if build.Phase != "build" || !containsArg(build.Runner, "--no-run") {
		t.Fatalf("first phase = %+v, want a --no-run build", build)
	}
	if runPhase.Phase != "run" || containsArg(runPhase.Runner, "--no-run") {
		t.Fatalf("second phase = %+v, want the real run", runPhase)
	}
}

// TestPostEdit_UnfinishedPhaseIsDeferredNotKilled pins the fix for 268
// timed-out edit runs: a phase still going at the budget keeps running, the
// hook says so in one line and returns, and the job is recorded for the next
// hook to report. Nothing is killed and nothing is thrown away.
func TestPostEdit_UnfinishedPhaseIsDeferredNotKilled(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	root := mkProject(t, "Cargo.toml")
	fakePhases(t) // nothing ever finishes

	got := PostEdit(postPayload("Edit", root+"/src/widget.rs"), fakeRun(true, "ok"))

	if !strings.Contains(got, "BUILDING") {
		t.Fatalf("advisory = %q, want the one-line BUILDING notice", got)
	}
	job, ok := loadDeferredJob(root)
	if !ok {
		t.Fatal("the deferred job must be recorded for the next hook")
	}
	if job.Phase != "build" || job.PID == 0 {
		t.Fatalf("recorded job = %+v, want the running build phase and its pid", job)
	}
	// A deferred build is not a timeout: nothing about the suite's speed has
	// been established, so the backoff streak must not move.
	s, _ := loadSession("sess-post")
	if ps := s.ByProject[root]; ps.TimeoutStreak != 0 {
		t.Fatalf("a deferred build must not stamp a timeout, got streak=%d", ps.TimeoutStreak)
	}
}

// TestPostEdit_HarvestsAFinishedDeferredBuild pins the payoff: the next hook
// finds the finished build, runs the (now warm) run phase, and reports the
// outcome as a deferred result rather than staying silent about work that
// completed after the previous hook returned.
func TestPostEdit_HarvestsAFinishedDeferredBuild(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	target := root + "/src/widget.rs"

	// A finished build phase, recorded against exactly this file's content.
	spawned := fakePhases(t, &PhaseOutcome{ExitCode: 0, Seconds: 42})
	saveDeferredJob(DeferredJob{
		Project: root, Phase: "build", Dir: root, PID: 999,
		Started: time.Now().Add(-time.Minute),
		HeadSHA: headSHAFor(root), FileHash: fileContentHash(target),
		Runner: []string{"cargo", "test", "--no-run"},
	})
	job, _ := loadDeferredJob(root)
	if err := os.WriteFile(job.Log, []byte("Compiling widget\nFinished"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 0, Seconds: 42})

	got := PostEdit(postPayload("Edit", target), fakeRun(true, "ok"))

	if !strings.Contains(got, "deferred") {
		t.Fatalf("advisory = %q, want it marked as a deferred result", got)
	}
	if len(*spawned) != 1 || (*spawned)[0].Phase != "run" {
		t.Fatalf("want the warm RUN phase started after harvesting the build, got %+v", *spawned)
	}
	if _, ok := loadDeferredJob(root); ok {
		t.Fatal("a harvested build's record must be cleared")
	}
}

// TestPostEdit_EditDuringADeferredBuildMarksItDirty pins the no-kill rule: a
// second edit to the same project must NOT kill the running build (cargo is
// doing real, incremental work) — it marks the job dirty so the harvest
// rebuilds for the newer source.
func TestPostEdit_EditDuringADeferredBuildMarksItDirty(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	target := root + "/src/widget.rs"
	spawned := fakePhases(t)

	saveDeferredJob(DeferredJob{
		Project: root, Phase: "build", Dir: root, PID: 4242,
		Started: time.Now().Add(-time.Minute), HeadSHA: headSHAFor(root), FileHash: "stale",
	})

	got := PostEdit(postPayload("Edit", target), fakeRun(true, "ok"))

	if !strings.Contains(got, "BUILDING") {
		t.Fatalf("advisory = %q, want the BUILDING notice while the build continues", got)
	}
	if len(*spawned) != 0 {
		t.Fatalf("a running build must not be duplicated, started %+v", *spawned)
	}
	job, ok := loadDeferredJob(root)
	if !ok || !job.Dirty {
		t.Fatalf("the running job must be marked dirty, got %+v (found=%v)", job, ok)
	}
	if job.PID != 4242 {
		t.Fatal("the running build must be left alone, not replaced")
	}
}

// TestPostEdit_AbandonsAJobPastTheMaximum pins the one kill: a phase that has
// outlived any plausible build is abandoned and a fresh one starts, so a
// wedged process cannot block a project forever.
func TestPostEdit_AbandonsAJobPastTheMaximum(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(deferredMaxEnv, "1")
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	root := mkProject(t, "Cargo.toml")
	spawned := fakePhases(t)

	var killed []int
	prevKill := killDeferredFn
	killDeferredFn = func(j DeferredJob) { killed = append(killed, j.PID) }
	t.Cleanup(func() { killDeferredFn = prevKill })

	saveDeferredJob(DeferredJob{
		Project: root, Phase: "build", Dir: root, PID: 777,
		Started: time.Now().Add(-time.Hour), HeadSHA: headSHAFor(root),
	})

	PostEdit(postPayload("Edit", root+"/src/widget.rs"), fakeRun(true, "ok"))

	if len(killed) != 1 || killed[0] != 777 {
		t.Fatalf("an over-age job must be abandoned, killed = %v", killed)
	}
	if len(*spawned) == 0 {
		t.Fatal("after abandoning the stale job a fresh phase must start")
	}
}

// TestHandlePrompt_ReportsAFinishedDeferredJob pins the second harvest
// point: a session that stops editing and just talks still gets told what
// the build it left running concluded.
func TestHandlePrompt_ReportsAFinishedDeferredJob(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	fakePhases(t)

	saveDeferredJob(DeferredJob{
		Project: root, Phase: "run", Dir: root, PID: 31337,
		Started: time.Now().Add(-time.Minute), HeadSHA: headSHAFor(root),
		Runner: []string{"cargo", "test"},
	})
	job, _ := loadDeferredJob(root)
	if err := os.WriteFile(job.Log, []byte("test result: FAILED. 1 failed\n--- widget::explodes"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 101, Seconds: 30})

	res := HandlePrompt([]byte(`{"prompt":"what now?","session_id":"s1","cwd":"` + filepath.ToSlash(root) + `"}`))
	if !strings.Contains(res.Message, "deferred") {
		t.Fatalf("prompt context = %q, want the finished deferred job reported", res.Message)
	}
	if _, ok := loadDeferredJob(root); ok {
		t.Fatal("a reported job must be cleared")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestPostEdit_NonCargoRunnersGetOneRunPhase pins the limit of the split:
// only cargo can build tests without running them. `go test --no-run` is not
// a flag — splitting there would turn every Go edit into an instant error, so
// a non-cargo runner gets exactly one phase, still deferrable.
func TestPostEdit_NonCargoRunnersGetOneRunPhase(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "go.mod")
	spawned := fakePhases(t, &PhaseOutcome{ExitCode: 0, Seconds: 1})

	PostEdit(postPayload("Edit", root+"/widget.go"), fakeRun(true, "ok"))

	if len(*spawned) != 1 {
		t.Fatalf("want exactly one phase for a go project, got %d: %+v", len(*spawned), *spawned)
	}
	if got := (*spawned)[0]; got.Phase != "run" || containsArg(got.Runner, "--no-run") {
		t.Fatalf("phase = %+v, want a single unsplit run", got)
	}
}
