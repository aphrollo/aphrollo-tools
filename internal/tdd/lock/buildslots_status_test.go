package lock

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestSnapshotBuildSlots_ReportsHolderAndIdle pins `gate status`'s slot
// table (issue #430): a held slot names its owner, an unheld one reads as
// idle — the whole point being to show every slot, not just the one that
// happened to block whichever edit hit QUEUED-SKIPPED.
func TestSnapshotBuildSlots_ReportsHolderAndIdle(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	dir := t.TempDir()

	_, release, ok := TryAcquireBuildSlot(dir, "cargo build -p server", "/repo/a")
	if !ok {
		t.Fatal("setup: expected the first slot to acquire")
	}
	defer release()

	snap := SnapshotBuildSlots()
	if len(snap) != 2 {
		t.Fatalf("expected 2 slots (APHROLLO_BUILD_SLOTS=2), got %d", len(snap))
	}
	var held, idle int
	for _, s := range snap {
		if s.Held {
			held++
			if s.Owner.Cmd != "cargo build -p server" || s.Owner.Cwd != "/repo/a" {
				t.Errorf("held slot owner = %+v, want the acquirer's own cmd/cwd", s.Owner)
			}
		} else {
			idle++
		}
	}
	if held != 1 || idle != 1 {
		t.Errorf("expected 1 held + 1 idle slot, got held=%d idle=%d", held, idle)
	}
}

// TestSnapshotBuildSlots_DeadOwnerPidReportsIdleNotHeld pins issue #435: the
// slot's own OS lock releases itself the instant its holder process dies, but
// the owner FILE beside it does not -- nothing removes it. A caller asking
// "what is running right now" must read that corpse as idle, the same
// distinction #451 already drew for deferred edit jobs -- otherwise a session
// waits on, or reports queued behind, a build that is not there any more.
func TestSnapshotBuildSlots_DeadOwnerPidReportsIdleNotHeld(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "1")

	// A pid this large cannot be a running process on any OS this box runs on
	// -- the same fictitious-but-real pid TestProcessStartToken_IsStableAndSpecificToTheProcess
	// uses, queried against the real OS rather than a mock.
	owner := BuildLockOwner{PID: 0x7FFFFFF0, Cmd: "cargo build -p server", Cwd: "/repo/a", Started: time.Now()}
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalSlotOwnerPath(0), data, 0o600); err != nil {
		t.Fatal(err)
	}

	snap := SnapshotBuildSlots()
	if len(snap) != 1 {
		t.Fatalf("expected 1 slot (APHROLLO_BUILD_SLOTS=1), got %d", len(snap))
	}
	if snap[0].Held {
		t.Fatalf("a dead pid's owner record read as held: %+v", snap[0])
	}
}
