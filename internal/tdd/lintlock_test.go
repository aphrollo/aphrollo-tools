package tdd

import (
	"testing"
	"time"
)

// withIsolatedLintLock points every lock this package owns at a per-test
// directory, the same way withIsolatedMutantsRunLock isolates the
// mutation-run lock: without it this test would contend with the box's own
// real lint lock, or with the aphrollo PostToolUse hook's own lint run
// exercising the SAME lock this very edit is adding.
func withIsolatedLintLock(t *testing.T) {
	t.Helper()
	restore := SetLockDirForTest(t.TempDir())
	t.Cleanup(restore)
}

// TestAcquireLintLock_SecondAcquirerWaitsWhileFirstHolds pins the reason
// this lock exists: two of THIS gate's own golangci-lint invocations must
// never reach golangci-lint's own machine-wide lock at the same moment —
// that collision is what surfaced as a spurious "lint failed" block even
// though --allow-serial-runners was already passed (the commit passed
// clean on retry, proving nothing was ever wrong with the code).
func TestAcquireLintLock_SecondAcquirerWaitsWhileFirstHolds(t *testing.T) {
	withIsolatedLintLock(t)
	release1, _, ok := AcquireLintLock("golangci-lint run ./a", "/repo/a", time.Second)
	if !ok {
		t.Fatal("setup: the first acquirer must get the lock immediately")
	}
	defer release1()

	if _, _, ok := AcquireLintLock("golangci-lint run ./b", "/repo/b", 150*time.Millisecond); ok {
		t.Fatal("a second lint must not acquire the box-wide lint lock while the first holds it")
	}
}

// TestAcquireLintLock_AcquiresOnceFirstReleases pins the other half: once
// the first lint's lock is released, a queued lint gets it promptly rather
// than staying refused — the "wait, then run" behaviour the fix is for,
// instead of "collide, then get reported as a lint failure".
func TestAcquireLintLock_AcquiresOnceFirstReleases(t *testing.T) {
	withIsolatedLintLock(t)
	release1, _, ok := AcquireLintLock("golangci-lint run ./a", "/repo/a", time.Second)
	if !ok {
		t.Fatal("setup: the first acquirer must get the lock immediately")
	}

	if _, _, ok := AcquireLintLock("golangci-lint run ./b", "/repo/b", 150*time.Millisecond); ok {
		t.Fatal("setup: the second acquirer must not succeed while the first still holds the lock")
	}

	release1()

	release2, waited, ok := AcquireLintLock("golangci-lint run ./b", "/repo/b", time.Second)
	if !ok {
		t.Fatal("a queued lint must acquire the lock once the first releases it")
	}
	if waited <= 0 {
		t.Fatal("a queued acquirer must report non-zero wait time")
	}
	release2()
}
