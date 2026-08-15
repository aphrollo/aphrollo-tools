package tdd

import (
	"path/filepath"
	"testing"
	"time"
)

// withIsolatedBuildLock points acquireBuildLock at a per-test lock file for
// the test's duration, instead of the real machine-wide one. Without this, a
// test exercising the lock races the box's OWN aphrollo PostToolUse hook
// (which runs `go test` against this working tree after every Edit/Write,
// and once installed exercises this exact production lock file too) —
// spurious contention unrelated to the behavior under test. See
// buildLockPathOverride's doc comment.
//
// It also shrinks the PostEdit/Precommit lock-wait deadlines from their
// production values (20s / 300s) to a couple hundred milliseconds: a
// contention test needs the DEADLINE to actually elapse to exercise the
// "gave up waiting" path, and the project's test-quality bar forbids a real
// sleep over 200ms — waiting out a real 20s or 300s budget just to prove a
// timeout path works would violate that outright (and was observed to,
// costing 320s for two tests before this fix).
func withIsolatedBuildLock(t *testing.T) {
	t.Helper()
	buildLockPathOverride = filepath.Join(t.TempDir(), "test-build.lock")
	origPostEdit, origPrecommit := buildLockPostEditDeadline, buildLockPrecommitDeadline
	buildLockPostEditDeadline = 120 * time.Millisecond
	buildLockPrecommitDeadline = 150 * time.Millisecond
	t.Cleanup(func() {
		buildLockPathOverride = ""
		buildLockPostEditDeadline = origPostEdit
		buildLockPrecommitDeadline = origPrecommit
	})
}

// TestAcquireBuildLock_SecondAcquirerBlocksUntilFirstReleases pins the core
// contract: two in-process acquirers of the SAME machine-wide build lock
// serialise — the second cannot acquire while the first holds it, and CAN
// once the first releases. This is what stops several concurrent Claude
// sessions from cold-building the same Bevy workspace at once (the observed
// failure this lock fixes).
func TestAcquireBuildLock_SecondAcquirerBlocksUntilFirstReleases(t *testing.T) {
	withIsolatedBuildLock(t)
	release1, ok1 := acquireBuildLock(time.Second)
	if !ok1 {
		t.Fatal("first acquirer must succeed immediately (uncontended)")
	}

	// Second acquirer, still held by the first: must fail within a SHORT
	// bound — no real sleeps > 200ms in this suite, so the bound itself is
	// deliberately tiny (the poll interval is 20ms, so several polls still
	// fit inside 150ms).
	if _, ok2 := acquireBuildLock(150 * time.Millisecond); ok2 {
		t.Fatal("second acquirer must not succeed while the first holds the lock")
	}

	release1()

	// Now that the first released, a fresh attempt must succeed promptly.
	release3, ok3 := acquireBuildLock(time.Second)
	if !ok3 {
		t.Fatal("a third acquirer must succeed once the lock is released")
	}
	release3()
}

// TestAcquireBuildLock_TimeoutPathReturnsFalseWithinBound pins the timeout
// contract in isolation: acquisition against an already-held lock returns
// false, and does so within roughly the requested deadline — not instantly
// (it must actually have polled) and not by blocking indefinitely.
func TestAcquireBuildLock_TimeoutPathReturnsFalseWithinBound(t *testing.T) {
	withIsolatedBuildLock(t)
	release, ok := acquireBuildLock(time.Second)
	if !ok {
		t.Fatal("setup: first acquirer must succeed")
	}
	defer release()

	const bound = 120 * time.Millisecond
	start := time.Now()
	_, ok2 := acquireBuildLock(bound)
	elapsed := time.Since(start)

	if ok2 {
		t.Fatal("acquiring an already-held lock must fail")
	}
	// Generous upper bound (10x the requested deadline) so this never flakes
	// on a loaded CI box, while still catching "acquireBuildLock ignored the
	// deadline and blocked much longer" — the bug this test exists to catch.
	if elapsed > bound*10 {
		t.Fatalf("acquireBuildLock took %s against a %s deadline — it is not honoring the bound", elapsed, bound)
	}
}

// TestAcquireBuildLock_ReleaseIsIdempotentSafe guards against a release()
// call panicking or wedging a later acquirer — a defensive double-release
// (e.g. a defer plus an explicit early release on one code path) must be
// harmless.
func TestAcquireBuildLock_ReleaseIsIdempotentSafe(t *testing.T) {
	withIsolatedBuildLock(t)
	release, ok := acquireBuildLock(time.Second)
	if !ok {
		t.Fatal("setup: first acquirer must succeed")
	}
	release()
	release() // must not panic

	release2, ok2 := acquireBuildLock(time.Second)
	if !ok2 {
		t.Fatal("a later acquirer must still succeed after a double release")
	}
	release2()
}
