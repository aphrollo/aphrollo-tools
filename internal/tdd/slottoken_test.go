package tdd

import (
	"path/filepath"
	"testing"
)

// TestSlotToken_ChildSkipsTheGlobalSlotButNotTheTargetLock pins the fix for a
// measured outage: `cargo mutants` runs up to four jobs in parallel, each of
// which invokes `cargo build`/`cargo test` through the shim, and each of THOSE
// took a global slot — so one mutation run held every slot on the box for
// hours and blocked every other session. A long verb now holds ONE slot for
// its whole run and hands its children a token. The token skips the GLOBAL
// semaphore only: each child still takes the lock for the target dir it builds
// in, because cargo's one-build-per-target invariant is what stops two
// compilers writing the same artifacts.
func TestSlotToken_ChildSkipsTheGlobalSlotButNotTheTargetLock(t *testing.T) {
	t.Setenv(buildSlotsEnv, "1")
	withIsolatedBuildLock(t)
	base := t.TempDir()
	parentTarget := filepath.Join(base, "outer", "target")
	copyA := filepath.Join(base, "mutants", "a", "target")
	copyB := filepath.Join(base, "mutants", "b", "target")

	// The long verb holds the box's ONE global slot for its whole run.
	parent, releaseParent, ok := TryAcquireBuildSlot(parentTarget)
	if !ok {
		t.Fatal("the long verb could not take a slot")
	}
	defer releaseParent()

	// Without the token a child is refused: the semaphore is full. That is
	// exactly the deadlock the token exists to break.
	if _, _, ok := TryAcquireBuildSlot(copyA); ok {
		t.Fatal("a tokenless child took a slot on a full box")
	}

	t.Setenv(slotTokenEnv, parent.Lock)

	childA, releaseA, ok := TryAcquireBuildSlot(copyA)
	if !ok {
		t.Fatal("a token child must build under its parent's slot")
	}
	defer releaseA()
	_, releaseB, ok := TryAcquireBuildSlot(copyB)
	if !ok {
		t.Fatal("a second token child in a DIFFERENT target must also proceed")
	}
	defer releaseB()

	if _, _, ok := TryAcquireBuildSlot(copyA); ok {
		t.Fatal("two builds were admitted into ONE target dir — the per-target lock still governs a token child")
	}
	if childA.Jobs <= 0 {
		t.Fatalf("child jobs = %d, want the slot's own cap", childA.Jobs)
	}
}

// TestSlotToken_ParentKeepsExactlyOneSlot pins the accounting: the point of
// the token is that a whole mutation run costs ONE slot, so a second
// unrelated build still gets the other one on a two-slot box.
func TestSlotToken_ParentKeepsExactlyOneSlot(t *testing.T) {
	t.Setenv(buildSlotsEnv, "2")
	withIsolatedBuildLock(t)
	base := t.TempDir()

	parent, releaseParent, ok := TryAcquireBuildSlot(filepath.Join(base, "outer", "target"))
	if !ok {
		t.Fatal("no slot for the long verb")
	}
	defer releaseParent()
	t.Setenv(slotTokenEnv, parent.Lock)
	for i, dir := range []string{"a", "b", "c"} {
		_, release, ok := TryAcquireBuildSlot(filepath.Join(base, dir, "target"))
		if !ok {
			t.Fatalf("token child %d refused — children must not consume slots", i)
		}
		defer release()
	}

	// The second slot is still free for someone else entirely.
	t.Setenv(slotTokenEnv, "")
	if _, release, ok := TryAcquireBuildSlot(filepath.Join(base, "other", "target")); !ok {
		t.Fatal("an unrelated build was starved: the long verb must cost exactly one slot")
	} else {
		release()
	}
}

// TestAcquireLongVerbSlot_ReleasesTheTargetBeforeTheLongPhase pins the shape
// the shim needs: the caller's target lock covers the prewarm compile only,
// while the global slot lasts for the whole run.
func TestAcquireLongVerbSlot_ReleasesTheTargetBeforeTheLongPhase(t *testing.T) {
	t.Setenv(buildSlotsEnv, "2")
	withIsolatedBuildLock(t)
	target := filepath.Join(t.TempDir(), "target")

	slot, releaseTarget, releaseAll, ok := TryAcquireLongVerbSlot(target)
	if !ok {
		t.Fatal("no long-verb slot")
	}
	if slot.Lock == "" {
		t.Fatal("the slot must name its lock, which is the token children carry")
	}
	releaseTarget()

	other, releaseOther, ok := TryAcquireBuildSlot(target)
	if !ok {
		t.Fatal("the caller's target must be free once the prewarm is done")
	}
	if other.Index == slot.Index {
		t.Fatal("the long verb still holds its global slot, so a new build must take a different one")
	}
	releaseOther()
	releaseAll()
}
