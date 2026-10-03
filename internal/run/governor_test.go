package run

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitFor polls until cond holds, failing the test after a generous bound.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		<-tick.C
	}
}

func TestSlots_IsTheSmallerOfThreadsAndMemoryEighthsClampedToOneThroughThree(t *testing.T) {
	cases := []struct {
		threads int
		freeMB  int64
		want    int
	}{
		{4, 100_000, 1},  // threads/8 = 0, raised to the floor
		{64, 1_024, 1},   // free GB / 8 = 0, raised to the floor
		{16, 24_576, 2},  // min(2, 3)
		{24, 16_384, 2},  // min(3, 2): memory is the smaller
		{24, 24_576, 3},  // min(3, 3)
		{64, 262_144, 3}, // min(8, 32), cut to the ceiling
		{8, 8_192, 1},    // min(1, 1)
	}
	for _, c := range cases {
		if got := Slots(c.threads, c.freeMB); got != c.want {
			t.Errorf("Slots(%d threads, %d MB) = %d, want %d", c.threads, c.freeMB, got, c.want)
		}
	}
}

func TestAdmit_NeedsFourGigabytesOfHeadroom(t *testing.T) {
	if !Admit(4096) {
		t.Error("Admit(4096 MB) = false, want true")
	}
	if Admit(4095) {
		t.Error("Admit(4095 MB) = true, want false")
	}
}

func acquire(t *testing.T, g *Governor, key string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	release, err := g.Acquire(ctx, key)
	if err != nil {
		t.Fatalf("Acquire(%q): %v", key, err)
	}
	return release
}

func TestGovernor_GrantsWaitersInArrivalOrder(t *testing.T) {
	g := NewGovernor(1)
	held := acquire(t, g, "")
	order := make(chan string, 2)
	start := func(name string, want int) {
		go func() {
			release := acquire(t, g, "")
			order <- name
			release()
		}()
		waitFor(t, name+" to queue", func() bool { return g.Waiting() == want })
	}
	start("first", 1)
	start("second", 2)

	held()

	for _, want := range []string{"first", "second"} {
		select {
		case got := <-order:
			if got != want {
				t.Fatalf("granted %q, want %q", got, want)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%q was never granted a slot", want)
		}
	}
}

func TestGovernor_ANewerRequestForTheSameKeyReplacesAQueuedOne(t *testing.T) {
	g := NewGovernor(1)
	held := acquire(t, g, "")
	older := make(chan error, 1)
	go func() {
		_, err := g.Acquire(context.Background(), "lane/unit")
		older <- err
	}()
	waitFor(t, "the older request to queue", func() bool { return g.Waiting() == 1 })
	newer := make(chan func(), 1)
	go func() { newer <- acquire(t, g, "lane/unit") }()

	select {
	case err := <-older:
		if !errors.Is(err, ErrSuperseded) {
			t.Fatalf("the older request got %v, want ErrSuperseded", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the older request was never replaced")
	}
	waitFor(t, "the newer request to queue", func() bool { return g.Waiting() == 1 })
	held()
	select {
	case release := <-newer:
		release()
	case <-time.After(20 * time.Second):
		t.Fatal("the newer request never got the slot")
	}
}

func TestGovernor_RequestsOfDifferentKeysAreAllKept(t *testing.T) {
	g := NewGovernor(1)
	held := acquire(t, g, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, key := range []string{"lane/a", "lane/b", "", ""} {
		go func() { _, _ = g.Acquire(ctx, key) }()
	}

	waitFor(t, "four distinct requests to queue", func() bool { return g.Waiting() == 4 })
	held()
}

func TestGovernor_ACancelledWaiterLeavesTheQueueAndKeepsNoSlot(t *testing.T) {
	g := NewGovernor(1)
	held := acquire(t, g, "")
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		_, err := g.Acquire(ctx, "")
		got <- err
	}()
	waitFor(t, "the waiter to queue", func() bool { return g.Waiting() == 1 })

	cancel()

	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire = %v, want context.Canceled", err)
	}
	if g.Waiting() != 0 {
		t.Fatalf("Waiting() = %d after the cancel, want 0", g.Waiting())
	}
	held()
	acquire(t, g, "")() // the slot is free again: a lost one would time this out
}

func TestGovernor_ReleaseTwiceFreesOneSlot(t *testing.T) {
	g := NewGovernor(2)
	first := acquire(t, g, "")
	acquire(t, g, "")
	first()
	first()
	acquire(t, g, "")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a third slot was granted past the two: err = %v", err)
	}
}

func TestNewGovernor_NeverHasFewerThanOneSlot(t *testing.T) {
	g := NewGovernor(0)

	acquire(t, g, "")() // a governor of no slots would time this out
}
