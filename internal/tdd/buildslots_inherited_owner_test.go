package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTryAcquireBuildSlot_AnInheritedSlotRecordsNoGlobalOwner pins the one
// place the two acquire paths disagreed. A child of a long verb builds under
// the slot its PARENT holds: it takes no global slot, and
// TryAcquireGlobalSlot's inherited branch deliberately writes no global
// owner record. TryAcquireBuildSlot's did — recordSlotOwner wrote
// globalSlotOwnerPath(s.Index) unconditionally, so an inherited slot
// (Index == inheritedSlotIndex, -1) left a record beside a slot file
// "…-slot.-1.lock" that nothing ever locks and no reader of the slot table
// can account for. The target-dir record is the one that means something
// here, and it must still be written.
func TestTryAcquireBuildSlot_AnInheritedSlotRecordsNoGlobalOwner(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	t.Setenv(slotTokenEnv, filepath.Join(t.TempDir(), "parent-build.lock"))

	target := filepath.Join(t.TempDir(), "target")
	slot, release, ok := TryAcquireBuildSlot(target, "cargo check -p server", "/repo")
	if !ok {
		t.Fatal("setup: a build inheriting its parent's slot must always get the target lock")
	}
	if slot.Index != inheritedSlotIndex {
		t.Fatalf("setup: slot index = %d, want the inherited marker %d", slot.Index, inheritedSlotIndex)
	}

	phantom := globalSlotOwnerPath(slot.Index)
	if _, err := os.Stat(phantom); err == nil {
		t.Errorf("an inherited slot wrote a global owner record at %s, beside a slot file nothing ever locks", phantom)
	}
	if _, err := os.Stat(slot.Owner); err != nil {
		t.Errorf("the target-dir record must still name who is building here: %v", err)
	}

	release()
	if _, err := os.Stat(slot.Owner); !os.IsNotExist(err) {
		t.Errorf("releasing must clear the target-dir record (stat err = %v)", err)
	}
	if _, err := os.Stat(phantom); err == nil {
		t.Errorf("releasing an inherited slot left a global owner record at %s", phantom)
	}
}

// TestTryAcquireBuildSlot_AnAcquiredSlotStillRecordsItsGlobalOwner is the
// other half: a build that really took one of the box's N slots is what the
// global record exists for — a waiter reading the slot table must be able to
// name who is using up the capacity.
func TestTryAcquireBuildSlot_AnAcquiredSlotStillRecordsItsGlobalOwner(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	t.Setenv(slotTokenEnv, "")

	target := filepath.Join(t.TempDir(), "target")
	slot, release, ok := TryAcquireBuildSlot(target, "cargo check -p server", "/repo")
	if !ok {
		t.Fatal("setup: the first build on an empty box must get a slot")
	}
	if slot.Index < 0 {
		t.Fatalf("setup: slot index = %d, want one of the box's own slots", slot.Index)
	}

	owner := globalSlotOwnerPath(slot.Index)
	if _, err := os.Stat(owner); err != nil {
		t.Errorf("a real global slot must record its holder at %s: %v", owner, err)
	}
	release()
	if _, err := os.Stat(owner); !os.IsNotExist(err) {
		t.Errorf("releasing must clear the global owner record (stat err = %v)", err)
	}
}
