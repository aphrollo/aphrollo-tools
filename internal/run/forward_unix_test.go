//go:build !windows

package run

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

func isLive(pid int) bool {
	liveGroups.mu.Lock()
	defer liveGroups.mu.Unlock()
	_, ok := liveGroups.pids[pid]
	return ok
}

func TestHeavy_IsInTheForwardersGroupsFromItsStartUntilItIsClosed(t *testing.T) {
	c, err := StartHeavy(context.Background(), helperSpec(t, "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	watch(t, []int{c.Pid()})
	if !isLive(c.Pid()) {
		t.Fatalf("heavy child %d is not among the groups a SIGINT or SIGTERM is forwarded to", c.Pid())
	}

	closeWithin(t, c)

	if isLive(c.Pid()) {
		t.Fatalf("closed child %d is still among the groups a signal is forwarded to: its pid may be reused", c.Pid())
	}
}

func TestLight_IsNeverInTheForwardersGroups(t *testing.T) {
	spec := helperSpec(t, "sleep")
	spec.Timeout = 30 * time.Second
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	watch(t, []int{c.Pid()})
	defer closeWithin(t, c)

	if isLive(c.Pid()) {
		t.Fatalf("light child %d is among the groups a signal is forwarded to, want only heavy ones", c.Pid())
	}
}

// The real send, over a real group: a SIGTERM to the group of a heavy child
// ends it. The helper handles only SIGINT, so SIGTERM takes its default.
func TestSignalGroup_DeliversTheSignalToTheChildsGroup(t *testing.T) {
	c, err := StartHeavy(context.Background(), helperSpec(t, "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	watch(t, []int{c.Pid()})
	defer closeWithin(t, c)

	if err := signalGroup(c.Pid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signalGroup = %v", err)
	}

	if !runtest.AllGone([]int{c.Pid()}, 10*time.Second) {
		t.Fatalf("child %d is still running 10 s after its group was sent SIGTERM", c.Pid())
	}
}

func TestSignalGroup_RefusesAnythingButAnOSSignal(t *testing.T) {
	if err := signalGroup(1, fakeSignal("x")); err == nil {
		t.Fatal("signalGroup accepted a signal that is not an OS signal")
	}
}
