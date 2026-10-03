package run

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeSignal is a signal that is none of the OS's: the forwarder never looks
// inside one, so a test proves it without sending this binary a real signal.
type fakeSignal string

func (f fakeSignal) String() string { return string(f) }
func (fakeSignal) Signal()          {}

// forwardRig is a forwarder over seams the test owns: the channel the watcher
// listens on, the signals it sent, and where it re-raised.
type forwardRig struct {
	mu      sync.Mutex
	fw      *forwarder
	ch      chan<- os.Signal
	armed   int
	stopped int
	sent    []sentSignal
	sendErr map[int]error
	raised  chan os.Signal
}

type sentSignal struct {
	pid int
	sig os.Signal
}

func newForwardRig() *forwardRig {
	r := &forwardRig{raised: make(chan os.Signal, 4), sendErr: map[int]error{}}
	r.fw = newForwarder(forwardSeams{
		notify: func(ch chan<- os.Signal) { r.mu.Lock(); r.ch = ch; r.armed++; r.mu.Unlock() },
		stop:   func(chan<- os.Signal) { r.mu.Lock(); r.stopped++; r.mu.Unlock() },
		send: func(pid int, sig os.Signal) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.sent = append(r.sent, sentSignal{pid, sig})
			return r.sendErr[pid]
		},
		reraise: func(sig os.Signal) { r.raised <- sig },
	})
	return r
}

func (r *forwardRig) deliver(t *testing.T, sig os.Signal) {
	t.Helper()
	r.mu.Lock()
	ch := r.ch
	r.mu.Unlock()
	if ch == nil {
		t.Fatal("the forwarder listens for no signal")
	}
	ch <- sig
}

func (r *forwardRig) awaitRaised(t *testing.T) os.Signal {
	t.Helper()
	select {
	case sig := <-r.raised:
		return sig
	case <-time.After(10 * time.Second):
		t.Fatal("the forwarder never re-raised the signal it forwarded")
		return nil
	}
}

func (r *forwardRig) counts() (armed, stopped int, sent []sentSignal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.armed, r.stopped, append([]sentSignal(nil), r.sent...)
}

func TestForwarder_SendsTheSignalToEveryLiveGroupInPidOrderThenRaisesItAgain(t *testing.T) {
	r := newForwardRig()
	r.fw.add(300)
	r.fw.add(100)
	r.fw.add(200)

	r.deliver(t, fakeSignal("sigterm"))
	got := r.awaitRaised(t)

	if got != fakeSignal("sigterm") {
		t.Errorf("re-raised %v, want the signal that arrived", got)
	}
	_, _, sent := r.counts()
	want := []sentSignal{{100, fakeSignal("sigterm")}, {200, fakeSignal("sigterm")}, {300, fakeSignal("sigterm")}}
	if len(sent) != len(want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Errorf("sent[%d] = %v, want %v", i, sent[i], want[i])
		}
	}
}

func TestForwarder_ASignalForOneGroupThatRefusesStillReachesTheRest(t *testing.T) {
	r := newForwardRig()
	r.sendErr[100] = errors.New("no such process")
	r.fw.add(100)
	r.fw.add(200)

	r.deliver(t, fakeSignal("sigint"))
	r.awaitRaised(t)

	if _, _, sent := r.counts(); len(sent) != 2 || sent[1].pid != 200 {
		t.Fatalf("sent %v, want both groups tried, 200 second", sent)
	}
}

func TestForwarder_ListensOnlyWhileAGroupIsLive(t *testing.T) {
	r := newForwardRig()
	if armed, _, _ := r.counts(); armed != 0 {
		t.Fatalf("armed %d times before any group, want 0: an idle process must keep the default signal behaviour", armed)
	}

	r.fw.add(100)
	r.fw.add(200)
	if armed, _, _ := r.counts(); armed != 1 {
		t.Fatalf("armed %d times for two groups, want 1", armed)
	}
	r.fw.remove(100)
	if _, stopped, _ := r.counts(); stopped != 0 {
		t.Fatalf("stopped listening with group 200 still live")
	}
	r.fw.remove(200)
	if _, stopped, _ := r.counts(); stopped != 1 {
		t.Fatalf("stopped %d times after the last group went, want 1", stopped)
	}
	r.fw.remove(200) // a second remove is a no-op
	if _, stopped, _ := r.counts(); stopped != 1 {
		t.Fatalf("a repeated remove stopped listening again: %d", stopped)
	}
}

func TestForwarder_AGroupThatEndedIsNotSignalled(t *testing.T) {
	r := newForwardRig()
	r.fw.add(100)
	r.fw.add(200)
	r.fw.remove(100)

	r.deliver(t, fakeSignal("sigterm"))
	r.awaitRaised(t)

	if _, _, sent := r.counts(); len(sent) != 1 || sent[0].pid != 200 {
		t.Fatalf("sent %v, want only group 200: pid 100 may be someone else's by now", sent)
	}
}

func TestForwarder_StepsAsideAfterASignalAndListensAgainForTheNextGroup(t *testing.T) {
	r := newForwardRig()
	r.fw.add(100)
	r.deliver(t, fakeSignal("sigterm"))
	r.awaitRaised(t)

	_, stopped, _ := r.counts()
	if stopped != 1 {
		t.Fatalf("stopped %d times after the signal, want 1: the re-raised signal must meet the default behaviour", stopped)
	}

	r.fw.add(200)
	if armed, _, _ := r.counts(); armed != 2 {
		t.Fatalf("armed %d times, want 2: a group started after a handled signal must be guarded too", armed)
	}
}
