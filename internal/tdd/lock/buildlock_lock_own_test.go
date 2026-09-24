package lock

import (
	"path/filepath"
	"testing"
	"time"
)

// TestPrecommitLockWaitAndPostEditLockWait_ReadTheSetterInstalledValue pins
// the reader/setter split: a package above lock (internal/cli) installs a
// budget through the exported setter, and every reader inside lock must see
// exactly that value, not a copy captured when the package loaded.
func TestPrecommitLockWaitAndPostEditLockWait_ReadTheSetterInstalledValue(t *testing.T) {
	defer SetPrecommitLockWait(90 * time.Second)()
	if got := precommitLockWait(); got != 90*time.Second {
		t.Fatalf("precommitLockWait() = %s, want the 90s the setter installed", got)
	}

	defer SetPostEditLockWaitForTest(3 * time.Second)()
	if got := postEditLockWait(); got != 3*time.Second {
		t.Fatalf("postEditLockWait() = %s, want the 3s the setter installed", got)
	}
}

// TestSetPrecommitLockWait_RestoreRoundTrips pins the restore half: calling
// the returned func puts the PREVIOUS value back, not a zero value — a test
// that stacks two overrides must be able to unwind them in order.
func TestSetPrecommitLockWait_RestoreRoundTrips(t *testing.T) {
	before := precommitLockWait()
	restore := SetPrecommitLockWait(before + time.Minute)
	if got := precommitLockWait(); got != before+time.Minute {
		t.Fatalf("precommitLockWait() after override = %s, want %s", got, before+time.Minute)
	}
	restore()
	if got := precommitLockWait(); got != before {
		t.Fatalf("precommitLockWait() after restore = %s, want the original %s back", got, before)
	}
}

// TestLockWaitLogAfter_ReadsTheSetterInstalledValue pins the third of the
// three budgets the same reader/setter split protects: a contended box that
// waits past this threshold before acquiring is what tells a "queued"
// commit from a broken suite in the log.
func TestLockWaitLogAfter_ReadsTheSetterInstalledValue(t *testing.T) {
	defer SetLockWaitLogThresholdForTest(2 * time.Second)()
	if got := lockWaitLogAfter(); got != 2*time.Second {
		t.Fatalf("lockWaitLogAfter() = %s, want the 2s the setter installed", got)
	}
}

// TestGoRaceLockKey_LivesUnderTheLockDir pins `-race`'s synthetic governor
// key to the same lock dir every other lock file resolves under (so
// SetLockDirForTest's one override isolates it too), and to a stable,
// repo-independent name — the whole point being one shared key across every
// lane on the box, never a per-repo directory.
func TestGoRaceLockKey_LivesUnderTheLockDir(t *testing.T) {
	dir := t.TempDir()
	defer SetLockDirForTest(dir)()

	if got, want := goRaceLockKey(), filepath.Join(dir, "go-race"); got != want {
		t.Fatalf("goRaceLockKey() = %q, want %q", got, want)
	}
}
