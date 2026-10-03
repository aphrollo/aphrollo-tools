// Package engine is the thin layer of docs/trellis-architecture.md (§2) between
// an adapter and the pure kernel: load the lane's checkpoint, let the kernel
// decide, save the checkpoint and append the event, and hand the decision back.
// It runs nothing and renders nothing: the effects of a decision are returned
// for the adapter to run once the lock is gone, and what comes of them returns
// as further events. It reaches the box only through the Store interface.
//
// A fact (a lane or run event, a verdict, an edit) steps the lane and TDD
// machines and is saved with the event that caused it. A question (kernel
// Kind.Question) is only decided: it moves no machine, saves no event and takes
// no lock, with one exception, the guided-once flag below.
//
// # Guided once
//
// A write about to happen (tool.pre) is decided by running the TDD machine over
// the edit it announces, to read the guide that edit would earn. The kernel
// does not return that step for a question, so the unit's "one guidance line
// per unit per lane" flag would never be set. The engine steps the machine with
// the same edit fact and keeps the flags only: the write has not happened, so
// its tree, phase and run request are not recorded; the post-edit event that
// follows records those.
//
// A flag goes on the unit's entry when a fact has made one. Otherwise it goes
// into Record.Guided, never into Units: an entry there would be a unit no fact
// has named, which a unit-less gated commit would seed with a last real
// verdict, and a question would have changed what the facts say. The first fact
// that names the unit moves its flags onto the new entry.
//
// # Seen
//
// What the agent has been told is kept in the record too (see seen.go): Deliver
// records a line written through a hook that reaches the agent, Unseen and
// Guidance hold back a line this actor already saw, and none of it is an event.
//
// # Concurrency
//
// Handle never holds a lock across the kernel call. It saves with the version it
// loaded; a lost update (ErrConflict) reloads and decides again, up to a bound.
// A fact that cannot be saved within the bound fails with ErrContended and
// returns no effects, so nothing runs for state that was not kept. A question
// whose flag cannot be saved still returns its decision: the worst case is that
// the line comes once more.
package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// DefaultAttempts is how many times Handle loads, decides and saves before it
// gives up on a lane that keeps changing under it.
const DefaultAttempts = 8

// Engine decides events for one repo. Its zero Attempts reads DefaultAttempts;
// Config is the repo's config as the adapter read it.
type Engine struct {
	Store    Store
	Config   kernel.Config
	Attempts int
}

// Handle decides one event. For a fact the returned Decision carries the
// machines' next state and the effects to run, and both are saved. For a
// question it carries the rule table's answer and no effects. A question that
// names no lane is decided against an empty lane and saves nothing; a fact that
// names none is ErrNoLane. An error from a question's flag save still returns
// the decision, alongside the error.
func (e *Engine) Handle(ctx context.Context, ev kernel.Event) (kernel.Decision, error) {
	question := ev.Kind.Question()
	if ev.Lane == "" {
		if !question {
			return kernel.Decision{}, ErrNoLane
		}
		return kernel.Decide(kernel.State{}, nil, ev, e.Config), nil
	}
	attempts := e.attempts()
	var last kernel.Decision
	for range attempts {
		if err := ctx.Err(); err != nil {
			return kernel.Decision{}, err
		}
		rec, version, err := e.Store.Load(ctx, ev.Lane)
		if err != nil {
			return kernel.Decision{}, fmt.Errorf("engine: load lane %q: %w", ev.Lane, err)
		}
		d := kernel.Decide(rec.Lane, rec.DecideUnits(ev.Unit), ev, e.Config)
		last = d
		next, events, save := Record{Lane: d.Lane, Units: d.Units, Guided: rec.Settled(ev.Unit), Delivered: rec.Delivered}, []kernel.Event{ev}, true
		if question {
			events = nil
			next, save = e.guided(rec, ev)
		}
		if !save {
			return d, nil
		}
		_, err = e.Store.Commit(ctx, ev.Lane, version, next, events)
		switch {
		case err == nil:
			return d, nil
		case errors.Is(err, ErrConflict):
			continue
		case question:
			return d, fmt.Errorf("engine: save lane %q: %w", ev.Lane, err)
		}
		return kernel.Decision{}, fmt.Errorf("engine: save lane %q: %w", ev.Lane, err)
	}
	if question {
		return last, nil
	}
	return kernel.Decision{}, fmt.Errorf("%w: lane %q after %d attempts", ErrContended, ev.Lane, attempts)
}

// guided is the record with the guided-once flag a write about to happen
// earns, and whether that changed anything. Only a tool.pre write announces an
// edit; every other question earns no flag. The flag goes on the unit's entry
// when a fact has made one, and into Guided when none has.
func (e *Engine) guided(rec Record, ev kernel.Event) (Record, bool) {
	if ev.Kind != kernel.KindPreTool || ev.Tool != kernel.ToolWrite || ev.Unit == "" {
		return rec, false
	}
	edit := ev
	edit.Kind = kernel.KindEdit
	before := rec.DecideUnits(ev.Unit)[ev.Unit]
	after := kernel.Decide(rec.Lane, rec.DecideUnits(ev.Unit), edit, e.Config).Units[ev.Unit]
	if (!after.GuidedUntested || before.GuidedUntested) && (!after.GuidedHeld || before.GuidedHeld) {
		return rec, false
	}
	f := Flags{Untested: before.GuidedUntested || after.GuidedUntested, Held: before.GuidedHeld || after.GuidedHeld}
	if u, known := rec.Units[ev.Unit]; known {
		u.GuidedUntested, u.GuidedHeld = f.Untested, f.Held
		rec.Units = maps.Clone(rec.Units)
		rec.Units[ev.Unit] = u
		return rec, true
	}
	rec.Guided = maps.Clone(rec.Guided)
	if rec.Guided == nil {
		rec.Guided = map[string]Flags{}
	}
	rec.Guided[ev.Unit] = f
	return rec, true
}
