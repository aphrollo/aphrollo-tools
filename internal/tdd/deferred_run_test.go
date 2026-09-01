package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// echoHeldEnv is a command that prints the build-lock-held marker's value, so
// a test can see what the phase's CHILD actually inherited.
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
