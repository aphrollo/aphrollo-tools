package run

import (
	"os"
	"slices"
	"sync"
)

// forwardSeams is everything the forwarder asks of the OS, so a test can
// stand in for each without a real signal.
type forwardSeams struct {
	// notify starts delivering the signals to forward to ch, stop ends it.
	notify func(ch chan<- os.Signal)
	stop   func(ch chan<- os.Signal)
	// send delivers sig to the process group that pid leads.
	send func(pid int, sig os.Signal) error
	// reraise delivers sig to this process again, once the forwarder no longer
	// listens for it.
	reraise func(sig os.Signal)
}

// forwarder carries a signal this process was sent on to the process group of
// every live heavy child. A child leads a group of its own, so the terminal's
// Ctrl-C never reaches it, and this process dying leaves the group running.
//
// It listens only while a group is live, so an idle process keeps Go's default
// disposition. When a signal arrives it forwards it, stops listening and
// raises it again: with nobody else listening the default ends this process
// just as it did before, and a handler another part of the process set up
// (a context cancelled on Ctrl-C, a cleanup before exit) still runs.
type forwarder struct {
	seams forwardSeams

	mu   sync.Mutex
	pids map[int]struct{}
	ch   chan os.Signal // nil while not listening
	done chan struct{}
}

func newForwarder(s forwardSeams) *forwarder {
	return &forwarder{seams: s, pids: map[int]struct{}{}}
}

// add puts the group led by pid under the forwarder.
func (f *forwarder) add(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pids[pid] = struct{}{}
	if f.ch == nil {
		f.arm()
	}
}

// remove takes the group out, for a child that has ended: its pid may belong
// to something else by now.
func (f *forwarder) remove(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pids, pid)
	if len(f.pids) == 0 && f.ch != nil {
		f.disarm()
	}
}

// arm starts listening. f.mu is held.
func (f *forwarder) arm() {
	ch, done := make(chan os.Signal, 1), make(chan struct{})
	f.ch, f.done = ch, done
	f.seams.notify(ch)
	go f.watch(ch, done)
}

// disarm stops listening. f.mu is held.
func (f *forwarder) disarm() {
	f.seams.stop(f.ch)
	close(f.done)
	f.ch, f.done = nil, nil
}

func (f *forwarder) watch(ch chan os.Signal, done chan struct{}) {
	select {
	case sig := <-ch:
		f.forward(sig, ch)
	case <-done:
	}
}

// forward sends sig to the live groups in pid order, then steps aside and
// raises it again. A group that refuses (it ended a moment ago) does not stop
// the others.
func (f *forwarder) forward(sig os.Signal, ch chan os.Signal) {
	f.mu.Lock()
	pids := make([]int, 0, len(f.pids))
	for pid := range f.pids {
		pids = append(pids, pid)
	}
	f.mu.Unlock()
	slices.Sort(pids)
	for _, pid := range pids {
		_ = f.seams.send(pid, sig)
	}
	f.mu.Lock()
	if f.ch == ch {
		f.disarm()
	}
	f.mu.Unlock()
	f.seams.reraise(sig)
}
