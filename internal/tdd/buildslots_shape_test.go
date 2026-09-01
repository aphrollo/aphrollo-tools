package tdd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBuildSlots_OneBuildPerTargetDir pins the constraint cargo itself
// imposes: a target dir admits exactly ONE build (cargo takes its own flock
// on the build directory). Admitting a second here would hand out a slot the
// second builder then spends its whole budget blocked on, invisibly, inside
// cargo — which is worse than being told to wait.
func TestBuildSlots_OneBuildPerTargetDir(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "4") // plenty of GLOBAL capacity...
	target := t.TempDir()

	_, release, ok := TryAcquireBuildSlot(target)
	if !ok {
		t.Fatal("the first build into a target dir must be admitted")
	}
	defer release()
	if _, _, ok := TryAcquireBuildSlot(target); ok {
		t.Fatal("...but a SECOND build into the SAME target dir must never be, whatever the global slot count")
	}
}

// TestBuildSlots_GlobalSemaphoreCapsConcurrentBuilds pins the other half:
// separate target dirs do not compete for a build directory, but they do
// compete for the box. N is the OOM/CPU governor — the N+1st build into a
// fresh target dir waits.
func TestBuildSlots_GlobalSemaphoreCapsConcurrentBuilds(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "2")

	_, relA, okA := TryAcquireBuildSlot(t.TempDir())
	_, relB, okB := TryAcquireBuildSlot(t.TempDir())
	if !okA || !okB {
		t.Fatal("two builds into two target dirs must both be admitted at N=2")
	}
	defer relA()
	if _, _, ok := TryAcquireBuildSlot(t.TempDir()); ok {
		t.Fatal("a third build must wait — the global slots are the box's capacity")
	}
	relB()
	_, relC, okC := TryAcquireBuildSlot(t.TempDir())
	if !okC {
		t.Fatal("once a global slot frees, the next build is admitted")
	}
	relC()
}

// TestBuildSlots_TargetLockReleasedWhenNoGlobalSlotIsFree pins the ordering
// rule that keeps the two locks from deadlocking each other: a build takes
// the target lock first and a global slot second, and if the second fails it
// must give the FIRST back — otherwise a full box would leave every target
// dir permanently locked by builds that never started.
func TestBuildSlots_TargetLockReleasedWhenNoGlobalSlotIsFree(t *testing.T) {
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "1")

	_, release, ok := TryAcquireBuildSlot(t.TempDir())
	if !ok {
		t.Fatal("setup: the one global slot must be takeable")
	}

	blocked := t.TempDir()
	if _, _, ok := TryAcquireBuildSlot(blocked); ok {
		t.Fatal("setup: the second build must be refused with the only global slot taken")
	}
	// The refusal must have left nothing behind.
	release()
	_, release2, ok := TryAcquireBuildSlot(blocked)
	if !ok {
		t.Fatal("the refused build's target lock must have been released — it was never held for a build that did not run")
	}
	release2()
}

// TestBuildSlotPaths_TargetKeyedAndGlobal pins the two file shapes an
// operator sees in %TEMP%: one lock per target dir (named by its hash) and N
// global slot files.
func TestBuildSlotPaths_TargetKeyedAndGlobal(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if targetLockPath(a) == targetLockPath(b) {
		t.Fatal("distinct target dirs must key to distinct target locks")
	}
	if base := filepath.Base(targetLockPath(a)); !strings.HasPrefix(base, "aphrollo-cargo-build.") {
		t.Fatalf("target lock name = %q, want aphrollo-cargo-build.<key>.lock", base)
	}
	if globalSlotPath(0) == globalSlotPath(1) {
		t.Fatal("global slots must be distinct files")
	}
	if base := filepath.Base(globalSlotPath(0)); !strings.HasPrefix(base, "aphrollo-cargo-slot.") {
		t.Fatalf("global slot name = %q, want aphrollo-cargo-slot.<i>.lock", base)
	}
}

// TestAcquireBuildSlot_AnnouncesTheQueueWhileItWaits pins the commit gate's
// visibility: a gate willing to wait twenty minutes must say who it is
// waiting for while it waits, not only when it gives up.
func TestAcquireBuildSlot_AnnouncesTheQueueWhileItWaits(t *testing.T) {
	withIsolatedBuildLock(t)
	target := t.TempDir()

	slot, release, ok := TryAcquireBuildSlot(target)
	if !ok {
		t.Fatal("setup: must be able to hold the target")
	}
	WriteBuildSlotOwner(slot, "cargo nextest run -p other-crate", "/some/other/repo")
	defer func() { RemoveBuildSlotOwner(slot); release() }()

	prev := buildLockQueueNoticeEvery
	buildLockQueueNoticeEvery = 30 * time.Millisecond
	t.Cleanup(func() { buildLockQueueNoticeEvery = prev })

	stderr := captureStderr(t, func() {
		acquireBuildSlot(target, 120*time.Millisecond)
	})
	if !strings.Contains(stderr, "queued behind") || !strings.Contains(stderr, "other-crate") {
		t.Fatalf("a waiting acquirer must name the holder while it waits, got: %q", stderr)
	}
}
