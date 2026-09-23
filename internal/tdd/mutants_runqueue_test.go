package tdd

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The box-wide mutation-run lock used to be polled: every waiter retried a
// non-blocking flock every 20ms, and whichever retry happened to land first
// after a release won. A merge that had waited 88 minutes lost to one started
// long after it. The queue these tests pin serves waiters in arrival order:
// only the earliest live ticket may try the lock at all.

// TestAcquireMutantsRunLock_LaterWaiterDoesNotOvertakeAnEarlierOne is the
// starvation itself. A waiter that arrived earlier is still waiting; the
// lock is free; a later waiter must not take it.
func TestAcquireMutantsRunLock_LaterWaiterDoesNotOvertakeAnEarlierOne(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	earlier := writeMutantsRunTicket(os.Getpid(), time.Now().Add(-time.Minute), "merge for PR 740", "/repo/earlier")

	if release, ok := acquireMutantsRunLockWithDeadline("merge for PR 748", "/repo/later", 300*time.Millisecond); ok {
		release()
		t.Fatal("a later waiter took the lock while an earlier live waiter was still queued ahead of it")
	}

	_ = os.Remove(earlier)
	release, ok := acquireMutantsRunLockWithDeadline("merge for PR 748", "/repo/later", 2*time.Second)
	if !ok {
		t.Fatal("once the earlier waiter left the queue, the later one must be served")
	}
	release()
}

// TestAcquireMutantsRunLock_DeadWaiterDoesNotBlockTheQueue pins liveness: a
// waiter killed while queued never removes its own ticket, and a ticket whose
// process is gone must not hold everyone behind it forever.
func TestAcquireMutantsRunLock_DeadWaiterDoesNotBlockTheQueue(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	const deadPID = 999_999_001
	t.Cleanup(SetPidRunningForTest(func(pid int) bool { return pid != deadPID && pidRunning(pid) }))
	writeMutantsRunTicket(deadPID, time.Now().Add(-time.Minute), "killed merge", "/repo/dead")

	release, ok := acquireMutantsRunLockWithDeadline("merge", "/repo/live", 2*time.Second)
	if !ok {
		t.Fatal("a ticket whose process is dead blocked the queue")
	}
	release()
}

// TestAcquireMutantsRunLock_SilentTicketDoesNotBlockTheQueue pins the other
// half of liveness: a pid can be reused by an unrelated process, so a ticket
// whose waiter stopped refreshing it is dead too, whatever its pid says.
func TestAcquireMutantsRunLock_SilentTicketDoesNotBlockTheQueue(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	ticket := writeMutantsRunTicket(os.Getpid(), time.Now().Add(-time.Hour), "merge whose pid was reused", "/repo/reused")
	old := time.Now().Add(-2 * mutantsRunTicketStaleAfter)
	if err := os.Chtimes(ticket, old, old); err != nil {
		t.Fatal(err)
	}

	release, ok := acquireMutantsRunLockWithDeadline("merge", "/repo/live", 2*time.Second)
	if !ok {
		t.Fatal("a ticket nobody refreshed blocked the queue")
	}
	release()
}

// TestAcquireMutantsRunLock_LeavesNoTicketBehind pins that a waiter leaves the
// queue whichever way its wait ends. A ticket left by a process that is still
// alive (it gave up and went on to do something else) would be live, and
// would block the queue for as long as that process runs.
func TestAcquireMutantsRunLock_LeavesNoTicketBehind(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	holder := acquireMutantsRunLock("holder", "/repo/holder")
	if _, ok := acquireMutantsRunLockWithDeadline("gave up", "/repo/gaveup", 100*time.Millisecond); ok {
		t.Fatal("setup: the lock is held, the bounded wait must give up")
	}
	holder()
	release, ok := acquireMutantsRunLockWithDeadline("served", "/repo/served", 2*time.Second)
	if !ok {
		t.Fatal("a waiter that gave up left a ticket that blocks the queue")
	}
	defer release()

	if q := SnapshotMutantsRun().Queue; len(q) != 0 {
		t.Fatalf("queue after one waiter gave up and another was served = %+v, want empty", q)
	}
}

// TestAcquireMutantsRunLock_AnnouncesItsPositionInTheQueue pins visibility: a
// waiter says where it stands and who holds the lock, not only that it waits.
func TestAcquireMutantsRunLock_AnnouncesItsPositionInTheQueue(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	release := acquireMutantsRunLock("mutants measure for /repo/holder", "/repo/holder")
	defer release()
	writeMutantsRunTicket(os.Getpid(), time.Now().Add(-time.Minute), "merge ahead", "/repo/ahead")

	stderr := captureStderr(t, func() {
		acquireMutantsRunLockWithDeadline("merge behind", "/repo/behind", 150*time.Millisecond)
	})

	if !strings.Contains(stderr, "position 2 of 2") || !strings.Contains(stderr, "/repo/holder") {
		t.Fatalf("a queued waiter must print its position and the holder, got: %q", stderr)
	}
}

// TestMutantsRunStatus_ListsTheQueueInArrivalOrder pins the status half: the
// report names every live waiter, earliest first.
func TestMutantsRunStatus_ListsTheQueueInArrivalOrder(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	now := time.Now()
	writeMutantsRunTicket(os.Getpid(), now.Add(-time.Minute), "merge second", "/repo/second")
	writeMutantsRunTicket(os.Getpid(), now.Add(-time.Hour), "merge first", "/repo/first")

	out := FormatMutantsRunStatus(SnapshotMutantsRun(), now)

	first, second := strings.Index(out, "/repo/first"), strings.Index(out, "/repo/second")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("the report must list both waiters, earliest first, got:\n%s", out)
	}
}

// The setter is the only way a test above core reaches the liveness probe, so
// it must both install the stub and put the real probe back: a restore that
// left the stub in place would call every later process alive.
func TestSetPidRunningForTest_StubIsSeenAndRestored(t *testing.T) {
	const deadPID = 999_999_002
	restore := SetPidRunningForTest(func(int) bool { return true })
	if !pidRunningFn(deadPID) {
		restore()
		t.Fatal("the stub was not installed: pidRunningFn still asks the OS")
	}
	restore()
	if pidRunningFn(deadPID) {
		t.Fatal("restore left the stub in place: a dead pid reads as running")
	}
}
