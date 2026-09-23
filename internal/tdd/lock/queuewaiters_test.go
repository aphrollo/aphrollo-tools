package lock

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestSnapshotQueueWaiters_ReportsLiveWaiter pins the write/read round trip:
// a waiter WriteQueueWaiter records is visible to SnapshotQueueWaiters with
// its own cmd, cwd and target, and gone once removed.
func TestSnapshotQueueWaiters_ReportsLiveWaiter(t *testing.T) {
	restore := SetLockDirForTest(t.TempDir())
	defer restore()

	remove := WriteQueueWaiter("/repo/a/target", "cargo build -p server", "/repo/a")

	got := SnapshotQueueWaiters()
	if len(got) != 1 {
		t.Fatalf("expected 1 waiter, got %d: %+v", len(got), got)
	}
	if got[0].Cmd != "cargo build -p server" || got[0].Cwd != "/repo/a" || got[0].Target != "/repo/a/target" {
		t.Fatalf("waiter = %+v, want the recorded cmd/cwd/target", got[0])
	}
	if got[0].PID != os.Getpid() {
		t.Errorf("waiter PID = %d, want this process's own pid %d", got[0].PID, os.Getpid())
	}

	remove()
	if got := SnapshotQueueWaiters(); len(got) != 0 {
		t.Fatalf("expected the waiter record gone after remove(), got %+v", got)
	}
}

// TestSnapshotQueueWaiters_DeadPidNotReportedAsBlocking pins issue #435's
// explicit requirement: a queue-waiter record whose process is dead must
// not be reported as still queued. Nothing removes the record when its
// process is killed rather than exiting cleanly, so a stale file would
// otherwise report a wait that ended hours ago as still blocking.
func TestSnapshotQueueWaiters_DeadPidNotReportedAsBlocking(t *testing.T) {
	restore := SetLockDirForTest(t.TempDir())
	defer restore()

	// A pid this large cannot be a running process on any OS this box runs
	// on -- the same fictitious-but-real pid
	// TestSnapshotBuildSlots_DeadOwnerPidReportsIdleNotHeld already uses
	// against the real OS liveness check.
	dead := QueueWaiter{PID: 0x7FFFFFF0, Cwd: "/repo/a", Cmd: "cargo build -p server", Target: "/repo/a/target", Started: time.Now()}
	if err := os.MkdirAll(queueWaitersDir(), 0o777); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(dead)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queueWaiterPath(dead.Target, dead.PID), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := SnapshotQueueWaiters(); len(got) != 0 {
		t.Fatalf("a dead pid's queue-waiter record was reported as still blocking: %+v", got)
	}
}

// TestQueueWaitersForRoot_ScopesToThisCheckout pins that a waiter recorded
// for a DIFFERENT checkout's cwd never shows up in this one's report -- the
// same scoping mutantsRunning/deferredBuildRunning already apply, so one
// lane's queue position is never read as another's.
func TestQueueWaitersForRoot_ScopesToThisCheckout(t *testing.T) {
	restore := SetLockDirForTest(t.TempDir())
	defer restore()

	removeMine := WriteQueueWaiter("/repo/a/target", "cargo build -p server", "/repo/a/crates/server")
	defer removeMine()
	removeOther := WriteQueueWaiter("/repo/b/target", "cargo build -p client", "/repo/b")
	defer removeOther()

	got := QueueWaitersForRoot("/repo/a")
	if len(got) != 1 || got[0].Cwd != "/repo/a/crates/server" {
		t.Fatalf("expected only /repo/a's own waiter, got %+v", got)
	}
}
