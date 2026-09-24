package lock

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Issue #830: four edits queued four identical full-tree builds behind one
// target dir. A request still waiting for its slot is replaced by a newer
// identical one (same target, same checkout, same command) instead of
// stacking behind it: the newer request builds the newer tree state, and the
// older one gives up saying why.

// queuedResult is what one acquireQueuedBuildSlot call came back with.
type queuedResult struct {
	wait    SlotWait
	release func()
	took    time.Duration
}

// observeQueued reports every request record acquireQueuedBuildSlot writes,
// so a test knows which request queued first without sleeping on it.
func observeQueued(t *testing.T) <-chan string {
	t.Helper()
	queued := make(chan string, 8)
	prev := slotRequestQueuedHook
	slotRequestQueuedHook = func(path string) { queued <- path }
	t.Cleanup(func() { slotRequestQueuedHook = prev })
	return queued
}

// startQueued runs one acquireQueuedBuildSlot call in the background and
// returns the channel its result arrives on.
func startQueued(target, cmd, cwd string, deadline time.Duration) <-chan queuedResult {
	done := make(chan queuedResult, 1)
	go func() {
		start := time.Now()
		_, release, wait := acquireQueuedBuildSlot(target, deadline, cmd, cwd)
		done <- queuedResult{wait: wait, release: release, took: time.Since(start)}
	}()
	return done
}

func TestAcquireQueuedBuildSlot_NewerIdenticalRequestReplacesAQueuedOne(t *testing.T) {
	withIsolatedBuildLock(t)
	queued := observeQueued(t)
	target := t.TempDir()
	_, holderRelease, ok := TryAcquireBuildSlot(target, "cargo nextest run -p forge_solver --no-run", "/ws")
	if !ok {
		t.Fatal("setup: must be able to hold the target")
	}

	older := startQueued(target, "cargo nextest run --no-run", "/ws", 5*time.Second)
	<-queued
	newer := startQueued(target, "cargo nextest run --no-run", "/ws", 5*time.Second)

	got := <-older
	if got.wait != SlotSuperseded {
		t.Fatalf("the older identical request came back %v, want %v", got.wait, SlotSuperseded)
	}
	if got.took > 2*time.Second {
		t.Fatalf("the older request waited %v, want it to leave as soon as the newer one queued", got.took)
	}

	holderRelease()
	won := <-newer
	if won.wait != SlotHeld {
		t.Fatalf("the newer request came back %v, want %v once the holder released", won.wait, SlotHeld)
	}
	won.release()
}

// A request for a different command is different work: it never replaces a
// queued one, which keeps waiting until its own deadline.
func TestAcquireQueuedBuildSlot_DifferentRequestDoesNotReplaceAQueuedOne(t *testing.T) {
	withIsolatedBuildLock(t)
	queued := observeQueued(t)
	target := t.TempDir()
	_, holderRelease, ok := TryAcquireBuildSlot(target, "cargo build", "/ws")
	if !ok {
		t.Fatal("setup: must be able to hold the target")
	}
	defer holderRelease()

	first := startQueued(target, "cargo nextest run -p forge_solver --no-run", "/ws", 300*time.Millisecond)
	<-queued
	second := startQueued(target, "cargo nextest run -p forge_render --no-run", "/ws", 300*time.Millisecond)

	if got := <-first; got.wait != SlotTimedOut {
		t.Fatalf("a queued request came back %v after a different one queued, want %v", got.wait, SlotTimedOut)
	}
	<-second
}

// A record left by a request whose process is gone replaces nothing: that
// request will never build, so the queued one keeps its place.
func TestAcquireQueuedBuildSlot_DeadNewerRequestDoesNotReplaceAQueuedOne(t *testing.T) {
	withIsolatedBuildLock(t)
	queued := observeQueued(t)
	defer SetPidRunningForTest(func(pid int) bool { return pid == os.Getpid() })()
	target := t.TempDir()
	_, holderRelease, ok := TryAcquireBuildSlot(target, "cargo build", "/ws")
	if !ok {
		t.Fatal("setup: must be able to hold the target")
	}
	defer holderRelease()

	waiting := startQueued(target, "cargo nextest run --no-run", "/ws", 300*time.Millisecond)
	path := <-queued
	dead, err := json.Marshal(slotRequest{PID: os.Getpid() + 1, Token: "dead-request"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, dead, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := <-waiting; got.wait != SlotTimedOut {
		t.Fatalf("a queued request came back %v after a dead process's record replaced its own, want %v", got.wait, SlotTimedOut)
	}
}

// A request that ended leaves no record: the record is what a later
// identical request is judged against, and one left behind by a finished
// request in a long-lived process would replace it on sight.
func TestAcquireQueuedBuildSlot_LeavesNoRecordOnceItEnds(t *testing.T) {
	withIsolatedBuildLock(t)
	queued := observeQueued(t)
	target := t.TempDir()

	held := startQueued(target, "cargo nextest run --no-run", "/ws", time.Second)
	path := <-queued
	got := <-held
	if got.wait != SlotHeld {
		t.Fatalf("an uncontended request came back %v, want %v", got.wait, SlotHeld)
	}
	got.release()

	if r, ok := readSlotRequest(path); ok {
		t.Fatalf("the ended request left its record behind: %+v", r)
	}
}
