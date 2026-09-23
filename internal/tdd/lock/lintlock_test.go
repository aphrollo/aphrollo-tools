package lock

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
	release1, _, _, ok := AcquireLintLock("golangci-lint run ./a", "/repo/a", time.Second)
	if !ok {
		t.Fatal("setup: the first acquirer must get the lock immediately")
	}
	defer release1()

	if _, _, _, ok := AcquireLintLock("golangci-lint run ./b", "/repo/b", 150*time.Millisecond); ok {
		t.Fatal("a second lint must not acquire the box-wide lint lock while the first holds it")
	}
}

// TestAcquireLintLock_AcquiresOnceFirstReleases pins the other half: once
// the first lint's lock is released, a queued lint gets it promptly rather
// than staying refused — the "wait, then run" behaviour the fix is for,
// instead of "collide, then get reported as a lint failure".
func TestAcquireLintLock_AcquiresOnceFirstReleases(t *testing.T) {
	withIsolatedLintLock(t)
	release1, _, _, ok := AcquireLintLock("golangci-lint run ./a", "/repo/a", time.Second)
	if !ok {
		t.Fatal("setup: the first acquirer must get the lock immediately")
	}

	if _, _, _, ok := AcquireLintLock("golangci-lint run ./b", "/repo/b", 150*time.Millisecond); ok {
		t.Fatal("setup: the second acquirer must not succeed while the first still holds the lock")
	}

	release1()

	release2, waited, _, ok := AcquireLintLock("golangci-lint run ./b", "/repo/b", time.Second)
	if !ok {
		t.Fatal("a queued lint must acquire the lock once the first releases it")
	}
	if waited <= 0 {
		t.Fatal("a queued acquirer must report non-zero wait time")
	}
	release2()
}

// TestAcquireLintLock_ReportsContendedWhenItsOwnFirstAttemptFails pins
// contended=true for the case that actually needs it: not two separate
// AcquireLintLock calls with an external release in between (the lock is
// simply free by the time the second call starts, so ITS first attempt
// succeeds and contended is correctly false — TestAcquireLintLock_-
// AcquiresOnceFirstReleases above), but ONE call whose own first attempt,
// inside its own loop, fails before a later attempt succeeds. Forcing that
// deterministically through acquireLintLockAttempt (rather than racing a
// goroutine's release() against this loop's own timing, which is exactly
// the kind of test that is flaky in CI) is what proves the FIRST-vs-later
// distinction the whole `contended` field exists to report.
func TestAcquireLintLock_ReportsContendedWhenItsOwnFirstAttemptFails(t *testing.T) {
	withIsolatedLintLock(t)
	real := acquireLintLockAttempt
	defer func() { acquireLintLockAttempt = real }()
	calls := 0
	acquireLintLockAttempt = func(cmd, cwd string) (func(), bool) {
		calls++
		if calls == 1 {
			return func() {}, false
		}
		return real(cmd, cwd)
	}

	release, waited, contended, ok := AcquireLintLock("golangci-lint run ./a", "/repo/a", time.Second)
	if !ok {
		t.Fatal("setup: the lock is free — the forced first failure must not stop the second attempt from succeeding")
	}
	defer release()
	if !contended {
		t.Fatal("an acquirer whose own first attempt failed must report contended=true")
	}
	if waited <= 0 {
		t.Fatal("an acquirer that had to retry must report non-zero wait time")
	}
	if calls < 2 {
		t.Fatalf("setup: acquireLintLockAttempt was called %d time(s), want at least 2", calls)
	}
}

// TestAcquireLintLock_ReportsUncontendedOnImmediateAcquire pins the other
// value of the SAME flag: a lock nobody else holds is acquired on the very
// first attempt, and must report contended=false. This is the fact
// internal/cli's `gate lint` wrapper prints "lock acquired after" from — a
// duration compared against a guessed threshold looked right but was not
// (real wall-clock time always advances some nonzero amount even on an
// uncontended first try), so the acquirer itself now says which happened.
func TestAcquireLintLock_ReportsUncontendedOnImmediateAcquire(t *testing.T) {
	withIsolatedLintLock(t)
	release, _, contended, ok := AcquireLintLock("golangci-lint run ./a", "/repo/a", time.Second)
	if !ok {
		t.Fatal("setup: an unheld lock must acquire immediately")
	}
	defer release()
	if contended {
		t.Fatal("an unheld lock's first attempt must report contended=false")
	}
}
