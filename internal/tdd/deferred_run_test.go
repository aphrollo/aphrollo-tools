package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// echoHeldEnv is a command that prints the build-lock-held marker's value, so
// a test can see what the phase's CHILD actually inherited.
func writeMarkerCmd(path string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo ran > " + path}
	}
	return []string{"sh", "-c", "echo ran > " + path}
}

func echoHeldEnv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo held=%" + BuildLockHeldEnv + "%"}
	}
	return []string{"sh", "-c", "echo held=$" + BuildLockHeldEnv}
}

// TestRunPhase_ChildSeesTheLockAsHeld pins the deadlock fix measured on
// D:/Projects/borld: the wrapper holds the build slot, then spawns cargo — and
// a session with the cargo-queue shim on PATH resolves "cargo" to the shim,
// which queued behind THE WRAPPER'S OWN slot record ("queued behind ... (pid
// 48848)") and sat there until it gave up. The child must be told the lock is
// already held by this process, exactly as runCargoLocked tells its own child.
func TestRunPhase_ChildSeesTheLockAsHeld(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	job := DeferredJob{
		Project: dir, Phase: "run", Dir: dir, Runner: echoHeldEnv(),
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json"),
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	jobPath := filepath.Join(dir, "job.json")
	if err := os.WriteFile(jobPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if code := RunPhase(jobPath); code != 0 {
		t.Fatalf("RunPhase exit = %d, want 0", code)
	}

	logged, err := os.ReadFile(job.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "held=1") {
		t.Fatalf("child saw %q, want held=1 — a nested cargo shim would queue behind this very phase", strings.TrimSpace(string(logged)))
	}
}

// writeJob saves a job record and returns the path the wrapper reads.
func writeJob(t *testing.T, j DeferredJob) string {
	t.Helper()
	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.Dir, "job.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunPhase_AlwaysWritesAResult pins the liveness contract from the OTHER
// side: the result file is the only thing that says a phase is over, so a
// wrapper that returns early (empty runner, unusable log path) leaves the
// next hook reporting BUILDING until the 600s ceiling for work that never
// ran.
func TestRunPhase_AlwaysWritesAResult(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: nil,
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}
	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done {
		t.Fatal("no result for a job the wrapper refused to run")
	}
	if out.ExitCode == 0 {
		t.Fatalf("result = %+v, want a failing exit for a phase that never ran", out)
	}
}

// TestRunPhase_FailsWhenItCannotHoldASlot pins the invariant the whole
// governor rests on: running the build anyway would compile into a target
// dir another build owns, and without the HELD marker the shimmed cargo
// inside would queue for the full wait on the slot this phase could not get.
func TestRunPhase_FailsWhenItCannotHoldASlot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(deferredMaxEnv, "1")
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")

	_, release, ok := TryAcquireBuildSlot(ResolveCargoTargetDir(dir), "cargo build", "/repo")
	if !ok {
		t.Fatal("could not occupy the slot")
	}
	defer release()

	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: writeMarkerCmd(marker),
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}
	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done || out.ExitCode == 0 {
		t.Fatalf("result = %+v (done %v), want a failure — no slot means no build", out, done)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the phase ran without a slot")
	}
}

// TestRunPhase_StampsStartedWhenItGetsTheSlot pins the clock the abandon
// rule reads: a phase that queued for the slot was already burning its
// 600s ceiling before it had begun, so a busy box could kill a build the
// moment it finally started.
func TestRunPhase_StampsStartedWhenItGetsTheSlot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	dir := t.TempDir()
	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: echoHeldEnv(),
		Started: time.Now().Add(-30 * time.Minute)}
	saveDeferredJob(j)
	saved, _ := loadDeferredJob("", dir)
	RunPhase(writeJob(t, saved))

	after, ok := loadDeferredJob("", dir)
	if !ok {
		t.Fatal("the job record vanished")
	}
	if time.Since(after.Started) > time.Minute {
		t.Fatalf("Started = %s, want it stamped when the slot was taken", after.Started)
	}
}

// TestDeferredSlotWait_IsAFractionOfTheMaximum pins the same defect at the
// budget level: queuing for the full ceiling leaves a phase eligible for the
// abandon kill the instant it starts to build.
func TestDeferredSlotWait_IsAFractionOfTheMaximum(t *testing.T) {
	t.Setenv(deferredMaxEnv, "600")
	if wait, max := deferredSlotWait(), deferredMax(); wait >= max {
		t.Fatalf("slot wait %s vs max %s — a phase must not be able to spend its whole life queuing", wait, max)
	}
}
