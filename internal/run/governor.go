package run

import (
	"context"
	"errors"
	"slices"
	"sync"
)

// Heavy children (builds, tests, lint, local CI, mutation) each hold a
// governor slot for as long as they run, so the box runs as many as it has
// room for and no more. Machine reads stay with the caller: Slots and Admit
// take a reading and answer, so every rule is provable against a box that
// does not exist.

const (
	// minSlots and maxSlots bound how many heavy children run at once.
	minSlots, maxSlots = 1, 3
	// threadsPerSlot and memoryPerSlotMB are what one slot is worth.
	threadsPerSlot  = 8
	memoryPerSlotMB = 8 * 1024
	// headroomNeededMB is the free memory a heavy child needs to start.
	headroomNeededMB = 4 * 1024
)

// ErrSuperseded is what a queued request gets when a newer one for the same
// key replaced it: the work it asked for has been asked for again.
var ErrSuperseded = errors.New("run: a newer request for the same key replaced this one in the queue")

// Slots is how many heavy children may run at once on a box with this many
// hardware threads and this much free memory: the smaller of threads/8 and
// free GB/8, from one to three.
func Slots(threads int, freeMB int64) int {
	return min(max(min(threads/threadsPerSlot, int(freeMB/memoryPerSlotMB)), minSlots), maxSlots)
}

// Admit reports whether a box with freeMB free has the headroom a heavy
// child needs to start: 4 GB.
func Admit(freeMB int64) bool { return freeMB >= headroomNeededMB }

// Governor hands out slots first in, first out.
type Governor struct {
	mu    sync.Mutex
	slots int
	used  int
	queue []*waiter
}

// waiter is one queued request. ready carries its answer: nil when it was
// granted a slot, ErrSuperseded when a newer request replaced it.
type waiter struct {
	key   string
	ready chan error
}

// NewGovernor is a governor with the given number of slots, at least one.
func NewGovernor(slots int) *Governor { return &Governor{slots: max(slots, minSlots)} }

// Waiting is how many requests are queued.
func (g *Governor) Waiting() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queue)
}

// Acquire takes a slot, waiting its turn until ctx ends, and returns the
// function that gives it back; calling that twice gives back one. A request
// with a key replaces a queued request of the same key, which gets
// ErrSuperseded; an empty key replaces nothing.
func (g *Governor) Acquire(ctx context.Context, key string) (func(), error) {
	g.mu.Lock()
	if g.used < g.slots {
		g.used++
		g.mu.Unlock()
		return g.releaser(), nil
	}
	if key != "" {
		if i := slices.IndexFunc(g.queue, func(w *waiter) bool { return w.key == key }); i >= 0 {
			g.queue[i].ready <- ErrSuperseded
			g.queue = slices.Delete(g.queue, i, i+1)
		}
	}
	w := &waiter{key: key, ready: make(chan error, 1)}
	g.queue = append(g.queue, w)
	g.mu.Unlock()

	select {
	case err := <-w.ready:
		return g.outcome(err)
	case <-ctx.Done():
		g.mu.Lock()
		g.queue = slices.DeleteFunc(g.queue, func(q *waiter) bool { return q == w })
		g.mu.Unlock()
		select {
		case err := <-w.ready:
			// The answer came as the context ended, and it stands: a slot
			// handed over is held, not dropped.
			return g.outcome(err)
		default:
			return nil, ctx.Err()
		}
	}
}

// outcome turns a waiter's answer into Acquire's.
func (g *Governor) outcome(err error) (func(), error) {
	if err != nil {
		return nil, err
	}
	return g.releaser(), nil
}

// releaser is the function that gives one slot back, once.
func (g *Governor) releaser() func() {
	var once sync.Once
	return func() { once.Do(g.free) }
}

// free hands the slot to the longest-waiting request, or returns it.
func (g *Governor) free() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.queue) == 0 {
		g.used--
		return
	}
	next := g.queue[0]
	g.queue = g.queue[1:]
	next.ready <- nil
}
