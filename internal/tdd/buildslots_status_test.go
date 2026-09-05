package tdd

import "testing"

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
