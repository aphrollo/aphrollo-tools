package suite

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
	res, _, acquired := runCargoLocked(stub, r, root, time.Second, time.Second, 0)
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
	if _, _, acquired := runCargoLocked(stub, r, crateRoot, time.Second, time.Second, 0); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if sawCwd != wsRoot {
		t.Fatalf("owner Cwd = %q, want the workspace root %q (Runner.Dir), not the crate root", sawCwd, wsRoot)
	}
}

// ratchet: test_removed TestRunCargoLocked_SetsAndRestoresBuildLockHeldEnv: the marker rides the runner's Env now, so the process environment is never written; TestRunCargoLocked_TellsTheChildTheLockIsHeld pins the same guard
// ratchet: test_removed TestRunCargoLocked_RestoresPriorBuildLockHeldEnvValue: there is no restore to test once the process environment is left alone; TestRunCargoLocked_LeavesAPriorBuildLockHeldValueAlone pins it

// TestRunCargoLocked_TellsTheChildTheLockIsHeld pins the deadlock guard
// (task A7): the child of a run holding the lock must see BuildLockHeldEnv=1,
// so a NESTED cargo invocation (resolved through the aphrollo cargo-queue
// shim, if a session prepended it to PATH) recognizes the lock is already
// held and passes straight through instead of deadlocking on it. The marker
// is the runner's own Env binding (suiteEnv applies it after everything
// inherited) and the process environment is never touched: a second build in
// the same process must not read this one's marker.
func TestRunCargoLocked_TellsTheChildTheLockIsHeld(t *testing.T) {
	withIsolatedBuildLock(t)

	var childSaw, processSaw string
	stub := func(r Runner, _ string) SuiteResult {
		childSaw = envValue(r.Env, BuildLockHeldEnv)
		processSaw = os.Getenv(BuildLockHeldEnv)
		return SuiteResult{Passed: true}
	}
	r := Runner{Cmd: "cargo", Args: []string{"test"}}
	if _, _, acquired := runCargoLocked(stub, r, t.TempDir(), time.Second, time.Second, 0); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if childSaw != "1" {
		t.Fatalf("the child's %s = %q, want \"1\"", BuildLockHeldEnv, childSaw)
	}
	if processSaw != "" {
		t.Fatalf("the process's %s during the run = %q, want it untouched (unset)", BuildLockHeldEnv, processSaw)
	}
}

// TestRunCargoLocked_LeavesAPriorBuildLockHeldValueAlone pins that a value
// already in the process environment (a nested aphrollo-in-aphrollo scenario)
// is neither overwritten nor cleared by the run.
func TestRunCargoLocked_LeavesAPriorBuildLockHeldValueAlone(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(BuildLockHeldEnv, "prior-value")

	var processSaw string
	stub := func(Runner, string) SuiteResult {
		processSaw = os.Getenv(BuildLockHeldEnv)
		return SuiteResult{Passed: true}
	}
	r := Runner{Cmd: "cargo", Args: []string{"test"}}
	if _, _, acquired := runCargoLocked(stub, r, t.TempDir(), time.Second, time.Second, 0); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if processSaw != "prior-value" {
		t.Fatalf("%s during the run = %q, want the process's own %q left alone", BuildLockHeldEnv, processSaw, "prior-value")
	}
	if got := os.Getenv(BuildLockHeldEnv); got != "prior-value" {
		t.Fatalf("%s after runCargoLocked returns = %q, want %q", BuildLockHeldEnv, got, "prior-value")
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
