package engine

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// rendezvous makes the first n loads wait until all n have read the record, so
// each of them holds the same version when it commits: the lost update a
// compare-and-swap exists to catch, forced rather than hoped for.
type rendezvous struct {
	Store
	n         int32
	arrived   atomic.Int32
	conflicts atomic.Int32
	ready     chan struct{}
}

func newRendezvous(s Store, n int32) *rendezvous {
	return &rendezvous{Store: s, n: n, ready: make(chan struct{})}
}

func (r *rendezvous) Load(ctx context.Context, lane string) (Record, uint64, error) {
	rec, ver, err := r.Store.Load(ctx, lane)
	if err != nil {
		return rec, ver, err
	}
	k := r.arrived.Add(1)
	if k == r.n {
		close(r.ready)
	}
	if k < r.n {
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		select {
		case <-r.ready:
		case <-waitCtx.Done():
			return Record{}, 0, fmt.Errorf("rendezvous: only %d of %d loads arrived", r.arrived.Load(), r.n)
		}
	}
	return rec, ver, nil
}

func (r *rendezvous) Commit(ctx context.Context, lane string, expect uint64, rec Record, events []kernel.Event) (uint64, error) {
	ver, err := r.Store.Commit(ctx, lane, expect, rec, events)
	if errors.Is(err, ErrConflict) {
		r.conflicts.Add(1)
	}
	return ver, err
}

// wait runs every job on its own goroutine and fails the test if they do not
// all finish within the bound.
func wait(t *testing.T, jobs []func() error) {
	t.Helper()
	errs := make(chan error, len(jobs))
	for _, j := range jobs {
		go func() { errs <- j() }()
	}
	ctx := bounded(t)
	for range jobs {
		select {
		case err := <-errs:
			if err != nil {
				t.Errorf("Handle: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("handlers did not finish within 30s")
		}
	}
}

func TestHandle_twoRacingFactsLoseNoUpdate(t *testing.T) {
	store := NewMemStore()
	gate := newRendezvous(store, 2)
	eng := &Engine{Store: gate}
	entered := func(actor string) kernel.Event {
		return kernel.Event{Kind: kernel.KindLaneEntered, Lane: "fix", Actor: actor, At: t0}
	}
	wait(t, []func() error{
		func() error { _, err := eng.Handle(bounded(t), entered("s1/a")); return err },
		func() error { _, err := eng.Handle(bounded(t), entered("s2/a")); return err },
	})
	rec, ver := load(t, store, "fix")
	if len(rec.Lane.Actors) != 2 {
		t.Errorf("actors = %v, want both s1/a and s2/a: the loser of the race must reload and fold its fact onto the winner's", rec.Lane.Actors)
	}
	if got := gate.conflicts.Load(); got != 1 {
		t.Errorf("conflicts = %d, want 1: both loads held version 0, so exactly one commit loses", got)
	}
	if ver != 2 || len(store.Events("fix")) != 2 {
		t.Errorf("version = %d with %d events, want 2 and 2", ver, len(store.Events("fix")))
	}
}

func TestHandle_manyRacingFactsLoseNoUpdate(t *testing.T) {
	const writers, perWriter = 6, 5
	store := NewMemStore()
	eng := &Engine{Store: store, Attempts: writers * perWriter}
	var jobs []func() error
	for w := range writers {
		jobs = append(jobs, func() error {
			for i := range perWriter {
				e := edit(fmt.Sprintf("pkg/%d", w), kernel.ClassCode, fmt.Sprintf("t%d", i))
				e.Actor = fmt.Sprintf("s%d/a", w)
				if _, err := eng.Handle(bounded(t), e); err != nil {
					return err
				}
			}
			return nil
		})
	}
	wait(t, jobs)
	rec, ver := load(t, store, "fix")
	if len(rec.Units) != writers || len(rec.Lane.Actors) != writers {
		t.Errorf("%d units and %d actors, want %d of each", len(rec.Units), len(rec.Lane.Actors), writers)
	}
	if want := uint64(writers * perWriter); ver != want || len(store.Events("fix")) != writers*perWriter {
		t.Errorf("version = %d with %d events, want %d of each", ver, len(store.Events("fix")), want)
	}
}

func TestMemStore_commitWithAStaleVersionIsRefused(t *testing.T) {
	store := NewMemStore()
	ctx := bounded(t)
	ver, err := store.Commit(ctx, "fix", 0, Record{Lane: kernel.State{Branch: "fix"}}, nil)
	if err != nil || ver != 1 {
		t.Fatalf("first Commit = %d, %v; want 1, nil", ver, err)
	}
	e := kernel.Event{Kind: kernel.KindEdit, Lane: "fix"}
	if _, err := store.Commit(ctx, "fix", 0, Record{}, []kernel.Event{e}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Commit err = %v, want ErrConflict", err)
	}
	if got := store.Events("fix"); len(got) != 0 {
		t.Errorf("log = %+v, want empty: a refused commit appends nothing", got)
	}
}

func TestMemStore_loadedRecordIsACopy(t *testing.T) {
	store := NewMemStore()
	ctx := bounded(t)
	rec := Record{Lane: kernel.State{Branch: "fix", Actors: map[string]time.Time{"s1/a": t0}}, Units: kernel.Units{"u": {Phase: kernel.PhaseOpen}}}
	if _, err := store.Commit(ctx, "fix", 0, rec, nil); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, _ := load(t, store, "fix")
	got.Lane.Actors["s2/a"] = t0
	got.Units["u"] = kernel.Unit{Phase: kernel.PhaseClosed}
	again, _ := load(t, store, "fix")
	if len(again.Lane.Actors) != 1 || again.Units["u"].Phase != kernel.PhaseOpen {
		t.Errorf("stored record changed through a loaded copy: %+v", again)
	}
}
