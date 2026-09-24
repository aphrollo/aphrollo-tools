package gc

import (
	"os"
	"path/filepath"
	"testing"
)

// occupyGlobalSlots takes every global build slot for target dirs that have
// nothing to do with repo, leaving the box at capacity for the memory
// governor while no candidate's OWN lock is held by anybody.
func occupyGlobalSlots(t *testing.T, n int) {
	t.Helper()
	for i := range n {
		dir := filepath.Join(t.TempDir(), "unrelated-target")
		_, release, ok := TryAcquireBuildSlot(dir, "cargo build -p server", "/some/other/repo")
		if !ok {
			t.Fatalf("setup: could not occupy global slot %d of %d", i+1, n)
		}
		t.Cleanup(release)
	}
}

// TestApplyGCFor_AFullBoxStillReclaimsAnUnlockedTargetDir pins the sweep's
// interlock to the one thing it is protecting against. The target-dir
// interlock went through TryAcquireBuildSlot, which needs a free GLOBAL slot
// as well as the target's own lock — so when unrelated builds hold every
// slot, no candidate's target lock was even attempted and every row came
// back skipped. That is precisely backwards: the box being full is when
// disk is the binding constraint, and deleting a directory nobody has locked
// consumes no memory at all, so it must not need the OOM governor's token.
func TestApplyGCFor_AFullBoxStillReclaimsAnUnlockedTargetDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	repo := t.TempDir()
	stray := filepath.Join(repo, "target-stray")
	mkFile(t, filepath.Join(stray, "debug", "artifact"), "1234567", 0)

	occupyGlobalSlots(t, 2)

	freed, refused, skipped := ApplyGCFor(repo, []GCCandidate{{
		Path: stray, Size: 7, Reason: "stray cargo target dir", Kind: GCKindStrayTarget,
	}})

	if len(refused) != 0 {
		t.Fatalf("refused %v", refused)
	}
	if skipped != 0 || freed != 7 {
		t.Fatalf("freed=%d skipped=%d, want the 7 bytes reclaimed: no build holds this target dir, and a full box is exactly when the sweep is needed most", freed, skipped)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("the target dir survived a sweep nothing was protecting it from (stat err = %v)", err)
	}
}

// TestApplyGCFor_TheTargetLockAloneDecidesNotTheOwnerRecord pins what makes
// the stale-owner escape (staleTargetLock) sound. That escape deletes a
// target dir whose recorded holder is provably dead, and it is the RIGHT
// question only when a failed acquire can mean one thing: another process
// holds THIS target's lock. So:
//   - lock FREE: the sweep takes it and deletes under it, whatever a
//     leftover owner record says — the record is not what protects a
//     directory, the lock is. A live pid in a record beside an unheld lock
//     is ordinary litter from a build that exited.
//   - lock HELD: the owner record decides, and a live holder keeps the dir.
//
// Both halves run with every global slot taken, so neither answer can come
// from the box's capacity.
func TestApplyGCFor_TheTargetLockAloneDecidesNotTheOwnerRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")
	repo := t.TempDir()
	occupyGlobalSlots(t, 2)

	sweep := func(dir string) (int64, int) {
		t.Helper()
		freed, refused, skipped := ApplyGCFor(repo, []GCCandidate{{
			Path: dir, Size: 7, Reason: "stray cargo target dir", Kind: GCKindStrayTarget,
		}})
		if len(refused) != 0 {
			t.Fatalf("refused %v", refused)
		}
		return freed, skipped
	}

	// Free lock, owner record naming a live process (this one).
	unlocked := filepath.Join(repo, "target-unlocked")
	mkFile(t, filepath.Join(unlocked, "debug", "artifact"), "1234567", 0)
	writeLockOwnerPid(t, ReadBuildSlotOwnerPath(unlocked), os.Getpid())
	if freed, skipped := sweep(unlocked); freed != 7 || skipped != 0 {
		t.Errorf("free lock: freed=%d skipped=%d, want the dir deleted — an owner record beside a lock nobody holds protects nothing", freed, skipped)
	}
	if _, err := os.Stat(unlocked); !os.IsNotExist(err) {
		t.Errorf("free lock: the dir survived (stat err = %v)", err)
	}

	// Held lock, same live owner record: now the record is answering for a
	// hold that really exists, and the dir stays.
	held := filepath.Join(repo, "target-held")
	mkFile(t, filepath.Join(held, "debug", "artifact"), "1234567", 0)
	release, ok := TryAcquireFileLock(targetLockPath(held))
	if !ok {
		t.Fatal("setup: could not take the target lock")
	}
	defer release()
	writeLockOwnerPid(t, ReadBuildSlotOwnerPath(held), os.Getpid())
	if freed, skipped := sweep(held); freed != 0 || skipped != 1 {
		t.Errorf("held lock: freed=%d skipped=%d, want the dir left for the next sweep", freed, skipped)
	}
	if _, err := os.Stat(held); err != nil {
		t.Errorf("held lock: deleted a target dir a live build holds: %v", err)
	}
}
