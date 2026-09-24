package lock

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Issue #830's second half: a full-tree build must not take a slot ahead of
// a per-crate build a source edit is waiting on. A full-tree request that is
// still queued does not try for the slot while a live package-scoped request
// waits for the same target dir.

// queueRecordFor writes a waiting request's record the way
// enqueueSlotRequest does, for a request this test does not run itself.
func queueRecordFor(t *testing.T, target, cmd, cwd string, pid int) {
	t.Helper()
	path := slotRequestPath(target, cmd, cwd)
	data, err := json.Marshal(slotRequest{PID: pid, Token: "waiting-" + cmd, Scoped: cargoPackageScoped(cmd)})
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureSharedSubdir(slotQueueDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireQueuedBuildSlot_FullTreeRequestYieldsToAQueuedPackageRequest(t *testing.T) {
	withIsolatedBuildLock(t)
	target := t.TempDir()
	queueRecordFor(t, target, "cargo nextest run -p forge_solver --lib --no-run", "/ws", os.Getpid())

	_, release, wait := acquireQueuedBuildSlot(target, 200*time.Millisecond, "cargo nextest run --no-run", "/ws")
	defer release()

	if wait != SlotTimedOut {
		t.Fatalf("a full-tree request came back %v with a package build queued ahead of it, want %v", wait, SlotTimedOut)
	}
}

// A package request that is waiting has no build to lose to another package
// request: both are the per-crate kind, and they keep first-come order.
func TestAcquireQueuedBuildSlot_PackageRequestDoesNotYieldToAnotherPackageRequest(t *testing.T) {
	withIsolatedBuildLock(t)
	target := t.TempDir()
	queueRecordFor(t, target, "cargo nextest run -p forge_render --lib --no-run", "/ws", os.Getpid())

	_, release, wait := acquireQueuedBuildSlot(target, 200*time.Millisecond, "cargo nextest run -p forge_solver --lib --no-run", "/ws")
	defer release()

	if wait != SlotHeld {
		t.Fatalf("a package request came back %v beside another package request, want %v", wait, SlotHeld)
	}
}

// A package request whose process is gone will never take the slot, a
// package request for another target dir does not want this one, and a
// full-tree request is not a package request.
func TestAcquireQueuedBuildSlot_FullTreeRequestIgnoresDeadForeignAndFullTreeRequests(t *testing.T) {
	withIsolatedBuildLock(t)
	defer SetPidRunningForTest(func(pid int) bool { return pid == os.Getpid() })()
	target := t.TempDir()
	queueRecordFor(t, target, "cargo nextest run -p forge_solver --lib --no-run", "/ws", os.Getpid()+1)
	queueRecordFor(t, t.TempDir(), "cargo nextest run -p forge_render --lib --no-run", "/other", os.Getpid())
	queueRecordFor(t, target, "cargo test --no-run", "/ws", os.Getpid())

	_, release, wait := acquireQueuedBuildSlot(target, 200*time.Millisecond, "cargo nextest run --no-run", "/ws")
	defer release()

	if wait != SlotHeld {
		t.Fatalf("a full-tree request came back %v with only a dead package request, a foreign one and another full-tree one queued, want %v", wait, SlotHeld)
	}
}

func TestCargoPackageScoped_ReadsThePackageFlagInEverySpelling(t *testing.T) {
	cases := map[string]bool{
		"cargo nextest run -p forge_solver --no-run":             true,
		"cargo test --package forge_solver --lib":                true,
		"cargo test --package=forge_solver":                      true,
		"cargo nextest run --no-run":                             false,
		"cargo nextest run --workspace --no-run":                 false,
		"cargo test --profile ci --no-run":                       false,
		"cargo nextest run -E test(/^physics::/) --lib --no-run": false,
	}
	for cmd, want := range cases {
		if got := cargoPackageScoped(cmd); got != want {
			t.Errorf("cargoPackageScoped(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// The mark a full-tree request yields to comes from the package request's
// own record: a queued per-crate build must say it is one.
func TestAcquireQueuedBuildSlot_PackageRequestMarksItsRecordScoped(t *testing.T) {
	withIsolatedBuildLock(t)
	queued := observeQueued(t)
	target := t.TempDir()
	_, holderRelease, ok := TryAcquireBuildSlot(target, "cargo nextest run --no-run", "/ws")
	if !ok {
		t.Fatal("setup: must be able to hold the target")
	}

	waiting := startQueued(target, "cargo nextest run -p forge_solver --lib --no-run", "/ws", 5*time.Second)
	r, read := readSlotRequest(<-queued)
	holderRelease()
	if got := <-waiting; got.wait == SlotHeld {
		got.release()
	}

	if !read || !r.Scoped {
		t.Fatalf("a queued package request left record %+v (read %v), want it marked scoped", r, read)
	}
}
