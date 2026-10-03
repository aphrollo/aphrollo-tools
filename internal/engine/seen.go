package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
)

// What the lane record keeps of what the agent has been told (architecture
// §5 "Guidance is given once per unit per lane", §11 F22-F23: "seen only after
// a delivery recorded as reaching the agent").
//
// A line counts as seen by an actor when Record.Delivered holds a delivery of
// that line's identity (render.Line.ID) to that actor, through a hook that
// reaches the agent. It is keyed by identity, not by a high-water mark of the
// lane's sequence: a mark would hide an unseen red behind a green of another
// unit delivered after it.
//
// # The bound
//
// The record is loaded on every hook, so what it keeps about deliveries is
// bounded: the newest MaxSeenPerActor lines of each actor, and the
// MaxSeenActors actors delivered to most recently. Pruning "lines whose unit
// closed" was rejected: a line's identity is a hash, so the record cannot tell
// which unit it belongs to, and a subagent's actor id is never reused, so its
// entries would outlive the lane's interest in them without a bound of their
// own. Whatever the bound forgets is due again, so the one cost of eviction is
// a line said twice, never a line lost. A repeat delivery of a kept line moves
// it to the newest place.
const (
	MaxSeenPerActor = 16
	MaxSeenActors   = 8
)

func (e *Engine) attempts() int {
	if e.Attempts < 1 {
		return DefaultAttempts
	}
	return e.Attempts
}

// Deliver records that lines were written to actor through hook. A hook whose
// output does not reach the agent (render.Reaches) records nothing and saves
// nothing, and neither does a line that says nothing. The delivery is saved
// with the same compare-and-swap Handle uses and retried on a lost update; it
// appends no event, for what an agent has read is derived state, not a fact of
// the lane. A delivery that cannot be saved within the attempts fails with
// ErrContended, and the caller's line is then said again, never lost.
func (e *Engine) Deliver(ctx context.Context, lane, actor string, hook render.Hook, lines []render.Line) error {
	if lane == "" {
		return ErrNoLane
	}
	if !render.Reaches(hook) {
		return nil
	}
	for range e.attempts() {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec, version, err := e.Store.Load(ctx, lane)
		if err != nil {
			return fmt.Errorf("engine: load lane %q: %w", lane, err)
		}
		next := recordDelivery(rec.Delivered, actor, hook, lines)
		if slices.Equal(next, rec.Delivered) {
			return nil
		}
		rec.Delivered = next
		_, err = e.Store.Commit(ctx, lane, version, rec, nil)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, ErrConflict):
			continue
		}
		return fmt.Errorf("engine: save lane %q: %w", lane, err)
	}
	return fmt.Errorf("%w: lane %q after %d attempts", ErrContended, lane, e.attempts())
}

// recordDelivery is the log after lines went to actor through hook: each line
// last, once, then the bounds applied.
func recordDelivery(log []render.Delivery, actor string, hook render.Hook, lines []render.Line) []render.Delivery {
	out := slices.Clone(log)
	for _, l := range lines {
		if l.Text == "" {
			continue
		}
		out = slices.DeleteFunc(out, func(d render.Delivery) bool { return d.Actor == actor && d.ID == l.ID })
		out = append(out, l.Deliver(hook, actor))
	}
	excess := 0
	for _, d := range out {
		if d.Actor == actor {
			excess++
		}
	}
	excess -= MaxSeenPerActor
	out = slices.DeleteFunc(out, func(d render.Delivery) bool {
		if d.Actor != actor || excess <= 0 {
			return false
		}
		excess--
		return true
	})
	newest := map[string]int{}
	for i, d := range out {
		newest[d.Actor] = i
	}
	if len(newest) <= MaxSeenActors {
		return out
	}
	byRecency := slices.SortedFunc(func(yield func(string) bool) {
		for a := range newest {
			if !yield(a) {
				return
			}
		}
	}, func(a, b string) int { return newest[a] - newest[b] })
	forgotten := byRecency[:len(byRecency)-MaxSeenActors]
	return slices.DeleteFunc(out, func(d render.Delivery) bool { return slices.Contains(forgotten, d.Actor) })
}

// Unseen is the lines due to actor, in the order given: every line that says
// something, that no reaching delivery to this actor covered, and that has not
// already appeared earlier in the same call. A line another line's delivery
// came after is not hidden by it; each is judged by its own identity. A lane
// that names no record yet has had nothing delivered.
func (e *Engine) Unseen(ctx context.Context, lane, actor string, lines []render.Line) ([]render.Line, error) {
	var log []render.Delivery
	if lane != "" {
		rec, _, err := e.Store.Load(ctx, lane)
		if err != nil {
			return nil, fmt.Errorf("engine: load lane %q: %w", lane, err)
		}
		log = rec.Delivered
	}
	var due []render.Line
	taken := map[string]bool{}
	for _, l := range lines {
		if render.Due(l, actor, log) && !taken[l.ID] {
			taken[l.ID] = true
			due = append(due, l)
		}
	}
	return due, nil
}

// Guidance decides one question and renders what it says to the event's actor.
// A deny is returned every time: a refusal repeats until it is obeyed. A guide
// is returned only if this actor has not seen it, on top of the kernel's own
// once-per-unit flag; an allow, or a guide already seen, returns the empty
// line, and the decision is the kernel's either way. The adapter writes the
// line, then records it with Deliver through the hook it wrote it to.
func (e *Engine) Guidance(ctx context.Context, ev kernel.Event, ref string) (kernel.Decision, render.Line, error) {
	d, err := e.Handle(ctx, ev)
	if d.Outcome == "" {
		return d, render.Line{}, err
	}
	line := render.Decision(d, ref)
	if line.Kind != render.KindGuide {
		return d, line, err
	}
	due, uerr := e.Unseen(ctx, ev.Lane, ev.Actor, []render.Line{line})
	switch {
	case uerr != nil:
		// An unreadable record must not hide the guide: it is said again.
		return d, line, errors.Join(err, uerr)
	case len(due) == 0:
		return d, render.Line{}, err
	}
	return d, line, err
}
