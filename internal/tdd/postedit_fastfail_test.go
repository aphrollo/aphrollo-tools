package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withIsolatedBuildLockKeepingDeadlines isolates the lock files but leaves
// the PRODUCTION lock-wait deadlines in place — the opposite of
// withIsolatedBuildLock, which shrinks them so contention tests can run.
// A test about how long the hooks are WILLING to wait has to see the real
// numbers, or it proves only that the test's own override is small.
func withIsolatedBuildLockKeepingDeadlines(t *testing.T) {
	t.Helper()
	t.Cleanup(setBuildLockPathOverride(filepath.Join(t.TempDir(), "test-build.lock")))
	t.Setenv(buildSlotsEnv, "1")
}

// TestAcquireBuildSlot_ZeroDeadlineIsOneTryNoSleep pins the edit hook's
// primitive: a zero deadline means try every slot ONCE and answer, never
// sleep. The poll interval is 20ms and the old post-edit budget was 20s, so
// a returned-instantly result is the only thing that distinguishes "single
// try" from "polled at least once".
func TestAcquireBuildSlot_ZeroDeadlineIsOneTryNoSleep(t *testing.T) {
	withIsolatedBuildLockKeepingDeadlines(t)
	target := t.TempDir()
	_, release, ok := TryAcquireBuildSlot(target)
	if !ok {
		t.Fatal("setup: the only slot must be takeable")
	}
	defer release()

	start := time.Now()
	if _, _, ok := acquireBuildSlot(target, 0); ok {
		t.Fatal("a saturated key must refuse a zero-deadline acquire")
	}
	if elapsed := time.Since(start); elapsed > buildLockPollInterval {
		t.Fatalf("zero-deadline acquire took %s — it slept, so it polled instead of trying once", elapsed)
	}
}

// TestPostEdit_QueuedSkippedIsImmediate pins the budget fix: an edit-time
// run whose target dir has no free slot must report QUEUED-SKIPPED AT ONCE.
// Before this, the hook spent 20s of its 100s budget waiting for a lock a
// multi-minute build was holding — a wait that costs the edit's whole test
// budget and, on a contended box, never succeeds anyway.
//
// Break this catches: buildLockPostEditDeadline going non-zero again (the
// production value is what this test runs with — see the helper).
func TestPostEdit_QueuedSkippedIsImmediate(t *testing.T) {
	withIsolatedBuildLockKeepingDeadlines(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")

	_, release, ok := acquireBuildSlot(resolveTargetDir(os.Getenv, root), time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the project's only build slot")
	}
	defer release()

	start := time.Now()
	got := PostEdit(postPayload("Edit", root+"/src/widget.rs"), func(Runner, string) SuiteResult {
		t.Error("the suite must never run while every slot is busy")
		return SuiteResult{Passed: true}
	})
	elapsed := time.Since(start)

	if !strings.Contains(got, "QUEUED-SKIPPED") {
		t.Fatalf("expected a QUEUED-SKIPPED advisory, got: %s", got)
	}
	// Generous next to the 20s this used to burn, tight enough that any
	// seconds-scale wait fails.
	if elapsed > 2*time.Second {
		t.Fatalf("the edit hook waited %s for a busy slot — it must fail fast, not spend its budget", elapsed)
	}
}

// TestSetPrecommitLockWait_BoundsTheCommitGatesWait pins the knob the CLI
// exposes as APHROLLO_LOCK_WAIT_SECS: a commit's cargo stage waits as long
// as the operator configured and no longer — and then REJECTS, rather than
// letting an untested commit land.
func TestSetPrecommitLockWait_BoundsTheCommitGatesWait(t *testing.T) {
	withIsolatedBuildLockKeepingDeadlines(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetPrecommitLockWait(80 * time.Millisecond))

	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	_, release, ok := acquireBuildSlot(resolvedDevTarget(root), time.Second)
	if !ok {
		t.Fatal("setup: must be able to take the gate target's only build slot")
	}
	defer release()

	start := time.Now()
	res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })
	elapsed := time.Since(start)

	if !res.Blocked {
		t.Fatalf("an untestable commit must be rejected, got: %s", res.Message)
	}
	// The production default is 300s; anything near that means the setter
	// was ignored.
	if elapsed > 10*time.Second {
		t.Fatalf("the commit gate waited %s against an 80ms configured wait — the knob is not wired", elapsed)
	}
}
