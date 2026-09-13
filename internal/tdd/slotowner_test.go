package tdd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A waiter's whole point is naming who it waits for. Every acquisition
// therefore has to leave an owner record, and the only way to guarantee that
// is for acquiring and recording to be ONE operation — a caller that has to
// remember a second call will eventually forget, which is how a merge came to
// print "queued behind another build (holder unknown)".
func TestEveryBuildSlotAcquirePathLeavesAReadableOwner(t *testing.T) {
	acquires := map[string]func(target string) (func(), bool){
		"TryAcquireBuildSlot": func(target string) (func(), bool) {
			_, release, ok := TryAcquireBuildSlot(target, "cargo build", "/repo")
			return release, ok
		},
		"TryAcquireLongVerbSlot": func(target string) (func(), bool) {
			_, _, releaseAll, ok := TryAcquireLongVerbSlot(target, "cargo mutants", "/repo")
			return releaseAll, ok
		},
		"acquireBuildSlot": func(target string) (func(), bool) {
			_, release, ok := acquireBuildSlot(target, 0, "cargo nextest run", "/repo")
			return release, ok
		},
	}
	for name, acquire := range acquires {
		dir := t.TempDir()
		t.Cleanup(SetBuildLockPathForTest(filepath.Join(dir, "aphrollo-build.lock")))
		target := filepath.Join(dir, "target")

		release, ok := acquire(target)
		if !ok {
			t.Fatalf("%s: could not acquire an uncontended slot", name)
		}
		owner, found := ReadBuildSlotOwner(target)
		if !found {
			t.Errorf("%s: acquired without recording an owner", name)
		} else if owner.Cwd != "/repo" || !strings.HasPrefix(owner.Cmd, "cargo ") {
			t.Errorf("%s: owner = %+v", name, owner)
		}
		release()
		if _, still := ReadBuildSlotOwner(target); still {
			t.Errorf("%s: the owner record outlived the lock", name)
		}
	}
}

// The observed failure: the TARGET was free and the box was at CAPACITY, so
// the waiter looked for a target owner that did not exist and said "holder
// unknown" about a build it could have named.
func TestHolderDescriptionNamesAGlobalSlotHolderWhenTheBoxIsAtCapacity(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetBuildLockPathForTest(filepath.Join(dir, "aphrollo-build.lock")))
	t.Setenv(buildSlotsEnv, "1")

	busy := filepath.Join(dir, "other-target")
	_, release, ok := TryAcquireBuildSlot(busy, "cargo test -p heavy", "/other/repo")
	if !ok {
		t.Fatal("could not take the only slot")
	}
	defer release()

	free := filepath.Join(dir, "my-target")
	if _, _, got := TryAcquireBuildSlot(free, "cargo build", "/repo"); got {
		t.Fatal("the box is at capacity — this must not acquire")
	}
	desc := buildSlotHolderDescription(free)
	if !strings.Contains(desc, "cargo test -p heavy") || !strings.Contains(desc, "/other/repo") {
		t.Errorf("holder description = %q, want the slot holder named", desc)
	}
}

func TestHolderDescriptionStillNamesTheTargetHolderDirectly(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetBuildLockPathForTest(filepath.Join(dir, "aphrollo-build.lock")))
	target := filepath.Join(dir, "target")

	_, release, ok := TryAcquireBuildSlot(target, "cargo build -p a", "/repo")
	if !ok {
		t.Fatal("could not acquire")
	}
	defer release()
	if desc := buildSlotHolderDescription(target); !strings.Contains(desc, "cargo build -p a") {
		t.Errorf("holder description = %q", desc)
	}
}

// A long verb hands its target dir back after the prewarm but keeps the
// global slot for hours. The slot owner is what a waiter blocked on capacity
// reads, so it must survive the target release.
func TestLongVerbSlotOwnerSurvivesTheTargetRelease(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetBuildLockPathForTest(filepath.Join(dir, "aphrollo-build.lock")))
	t.Setenv(buildSlotsEnv, "1")
	target := filepath.Join(dir, "target")

	_, releaseTarget, releaseAll, ok := TryAcquireLongVerbSlot(target, "cargo mutants", "/repo")
	if !ok {
		t.Fatal("could not acquire")
	}
	releaseTarget()

	other := filepath.Join(dir, "other")
	if _, _, got := TryAcquireBuildSlot(other, "cargo build", "/repo2"); got {
		t.Fatal("the only slot is still held by the long verb")
	}
	if desc := buildSlotHolderDescription(other); !strings.Contains(desc, "cargo mutants") {
		t.Errorf("holder description = %q, want the long verb named", desc)
	}
	releaseAll()
}

func TestRunCargoLockedRecordsItsOwnerWhileItRuns(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(SetBuildLockPathForTest(filepath.Join(dir, "aphrollo-build.lock")))
	root := t.TempDir()

	var seen bool
	run := func(r Runner, dir string) SuiteResult {
		if o, ok := ReadBuildSlotOwner(ResolveCargoTargetDir(root)); ok && strings.Contains(o.Cmd, "nextest") {
			seen = true
		}
		return SuiteResult{}
	}
	runCargoLocked(run, Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}, root, 0, time.Second, 0)
	if !seen {
		t.Error("the suite ran without a readable owner record")
	}
}
