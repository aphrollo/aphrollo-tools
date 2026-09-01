package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunCargoLocked_WritesAndRemovesOwnerFile pins task A7's owner-file
// contract: a cargo runner that goes through runCargoLocked must record WHO
// holds the lock (pid/cwd/cmd/started) so a waiting acquirer (the cargo
// shim, or a hook's own QUEUED-SKIPPED line) can name the holder instead of
// reporting a bare "someone else has it". The owner file must exist WHILE
// run() executes and be gone once runCargoLocked returns.
func TestRunCargoLocked_WritesAndRemovesOwnerFile(t *testing.T) {
	withIsolatedBuildLock(t)

	var sawOwner BuildLockOwner
	var sawOK bool
	root := t.TempDir()
	stub := func(Runner, string) SuiteResult {
		sawOwner, sawOK = ReadBuildSlotOwner(resolveTargetDir(os.Getenv, root))
		return SuiteResult{Passed: true}
	}

	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "widget"}}
	res, _, acquired := runCargoLocked(stub, r, root, time.Second, time.Second)
	if !acquired || !res.Passed {
		t.Fatalf("expected the lock to be acquired and the stub to run, acquired=%v res=%+v", acquired, res)
	}
	if !sawOK {
		t.Fatal("expected an owner file to exist WHILE the suite ran, found none")
	}
	if sawOwner.PID != os.Getpid() {
		t.Errorf("owner PID = %d, want this process's PID %d", sawOwner.PID, os.Getpid())
	}
	if sawOwner.Cwd != root {
		t.Errorf("owner Cwd = %q, want %q", sawOwner.Cwd, root)
	}
	if !strings.Contains(sawOwner.Cmd, "cargo") || !strings.Contains(sawOwner.Cmd, "widget") {
		t.Errorf("owner Cmd = %q, want it to name the actual cargo command", sawOwner.Cmd)
	}
	if sawOwner.Started.IsZero() {
		t.Error("owner Started must be set")
	}

	if _, ok := ReadBuildSlotOwner(resolveTargetDir(os.Getenv, root)); ok {
		t.Fatal("owner file must be removed once runCargoLocked returns")
	}
}

// TestRunCargoLocked_OwnerFileUsesRunnerDir pins that the recorded Cwd
// follows Runner.Dir (the resolved cargo workspace root, task A4) when set,
// not the crate root passed as the root parameter — the owner file must
// describe where the command ACTUALLY runs.
func TestRunCargoLocked_OwnerFileUsesRunnerDir(t *testing.T) {
	withIsolatedBuildLock(t)

	wsRoot := t.TempDir()
	crateRoot := filepath.Join(wsRoot, "crates", "a")

	var sawCwd string
	stub := func(Runner, string) SuiteResult {
		if o, ok := ReadBuildSlotOwner(resolveTargetDir(os.Getenv, wsRoot)); ok {
			sawCwd = o.Cwd
		}
		return SuiteResult{Passed: true}
	}

	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}, Dir: wsRoot}
	if _, _, acquired := runCargoLocked(stub, r, crateRoot, time.Second, time.Second); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if sawCwd != wsRoot {
		t.Fatalf("owner Cwd = %q, want the workspace root %q (Runner.Dir), not the crate root", sawCwd, wsRoot)
	}
}

// TestRunCargoLocked_SetsAndRestoresBuildLockHeldEnv pins the deadlock guard
// (task A7): while runCargoLocked holds the lock and the stub runs, the
// BuildLockHeldEnv variable must read "1" in the CURRENT process's own
// environment — RunSuite's suiteEnv() inherits os.Environ(), so this is what
// lets a NESTED cargo invocation (resolved through the aphrollo cargo-queue
// shim, if a session prepended it to PATH) recognize the lock is already
// held by this process and pass straight through instead of deadlocking on
// it. The env var must be restored to whatever it was before (unset, or a
// pre-existing value) once runCargoLocked returns.
func TestRunCargoLocked_SetsAndRestoresBuildLockHeldEnv(t *testing.T) {
	withIsolatedBuildLock(t)

	if _, had := os.LookupEnv(BuildLockHeldEnv); had {
		t.Fatalf("test precondition: %s must not be set before this test", BuildLockHeldEnv)
	}

	var sawDuringRun string
	stub := func(Runner, string) SuiteResult {
		sawDuringRun = os.Getenv(BuildLockHeldEnv)
		return SuiteResult{Passed: true}
	}
	r := Runner{Cmd: "cargo", Args: []string{"test"}}
	if _, _, acquired := runCargoLocked(stub, r, t.TempDir(), time.Second, time.Second); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if sawDuringRun != "1" {
		t.Fatalf("%s during the run = %q, want \"1\"", BuildLockHeldEnv, sawDuringRun)
	}
	if after := os.Getenv(BuildLockHeldEnv); after != "" {
		t.Fatalf("%s after runCargoLocked returns = %q, want unset (restored)", BuildLockHeldEnv, after)
	}
}

// TestRunCargoLocked_RestoresPriorBuildLockHeldEnvValue guards the restore
// path when the env var was ALREADY set to some other value beforehand (e.g.
// a nested aphrollo-in-aphrollo scenario) -- runCargoLocked must put back the
// EXACT prior value, not just unset it.
func TestRunCargoLocked_RestoresPriorBuildLockHeldEnvValue(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(BuildLockHeldEnv, "prior-value")

	stub := func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }
	r := Runner{Cmd: "cargo", Args: []string{"test"}}
	if _, _, acquired := runCargoLocked(stub, r, t.TempDir(), time.Second, time.Second); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if got := os.Getenv(BuildLockHeldEnv); got != "prior-value" {
		t.Fatalf("%s after runCargoLocked returns = %q, want the restored prior value %q", BuildLockHeldEnv, got, "prior-value")
	}
}

// TestReadBuildSlotOwner_NoFileMeansNotOK guards the "no owner recorded" case
// (no cargo has ever run through runCargoLocked against this lock, or a
// pre-A7 build didn't write one): ReadBuildSlotOwner must report ok=false,
// not a zero-valued "owner" that could be mistaken for a real one.
func TestReadBuildSlotOwner_NoFileMeansNotOK(t *testing.T) {
	withIsolatedBuildLock(t)
	if _, ok := ReadBuildSlotOwner(t.TempDir()); ok {
		t.Fatal("expected no owner file to exist for a fresh isolated lock path")
	}
}
