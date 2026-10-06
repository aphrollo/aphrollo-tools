package shadow

import (
	"context"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// Clock is the time a record's budget is kept in. The hook's is the wall clock; a
// test's moves only when it says so, so a slow probe is a call that advances it and
// the budget is judged on what the work cost, never on how loaded the box ran the
// test.
type Clock interface {
	Now() time.Time
	// Timer answers a channel that receives once d has passed, and the call that
	// releases it.
	Timer(d time.Duration) (<-chan time.Time, func())
}

// clock is the clock the budget runs on, a seam for the tests of this package.
var clock Clock = wallClock{}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) Timer(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

// Prefetch is work started ahead of the record's window: a probe the record needs
// the answer of (where a waived write lands asks git) is begun when the hook has
// what it needs to ask it, and the hook's own work overlaps it. The window then
// only collects the answer, so what the probe cost is not spent out of the budget.
type Prefetch[T any] struct {
	done chan struct{}
	v    T
}

// StartPrefetch begins fn now, on its own goroutine. A panic in fn is dropped, as
// the record's own work is: the answer is then the zero value.
func StartPrefetch[T any](fn func() T) *Prefetch[T] {
	p := &Prefetch[T]{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		defer func() { _ = recover() }()
		p.v = fn()
	}()
	return p
}

// Done is closed once the work has finished.
func (p *Prefetch[T]) Done() <-chan struct{} { return p.done }

// Wait is the answer, waiting for the work no longer than ctx lasts: false and the
// zero value when the window closed first.
func (p *Prefetch[T]) Wait(ctx context.Context) (T, bool) {
	select {
	case <-p.done:
		return p.v, true
	default:
	}
	select {
	case <-p.done:
		return p.v, true
	case <-ctx.Done():
		var zero T
		return zero, false
	}
}

// DropGrace is how long a hook waits to write the record of what it dropped, on top
// of the budget: the record of a drop is one small append, and a writer that cannot
// do it in this time is left behind like any other, never waited for.
var DropGrace = 50 * time.Millisecond

// appendDropped writes the records of what a hook dropped (unjudged records naming
// the budget) within one DropGrace on the clock for all of them, in order on one
// goroutine: however many were dropped, the hook waits once. Those not written when
// the grace ends are left to the goroutine. The wait is counted for TakeWaited.
func appendDropped(evs ...core.Event) {
	if len(evs) == 0 {
		return
	}
	clk := clock
	start := clk.Now()
	defer func() { waited.Add(int64(clk.Now().Sub(start))) }()
	// The grace is armed before the writer starts, so a writer that is stuck on its first
	// record always has a grace running that the clock can end.
	grace, release := clk.Timer(DropGrace)
	defer release()
	write := appendEvent // read now: the goroutine below may outlive a test that swaps it
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		for _, e := range evs {
			write(e)
		}
	}()
	select {
	case <-done:
	case <-grace:
	}
}
