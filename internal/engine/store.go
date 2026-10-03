package engine

import (
	"context"
	"errors"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
)

var (
	// ErrConflict is what a Store answers a Commit whose expected version is no
	// longer the stored one: someone else saved the lane since it was loaded.
	ErrConflict = errors.New("engine: lane record changed since it was loaded")
	// ErrContended is Handle giving up on a fact after its attempts all lost.
	ErrContended = errors.New("engine: lane record kept changing, gave up")
	// ErrNoLane is a fact that names no lane: there is no record to keep it in.
	ErrNoLane = errors.New("engine: event names no lane")
)

// Record is one lane's checkpoint as far as the kernel is concerned: the lane
// machine's state and the TDD machine's units (architecture §3 "Lane record").
type Record struct {
	Lane  kernel.State
	Units kernel.Units

	// Delivered is what the agent has been told, oldest first: the lines written
	// through a hook that reaches it, bounded (see MaxSeenPerActor).
	Delivered []render.Delivery
}

// Store is what the engine needs of the per-repo store (architecture §8; the
// real one is F24 to F27). Keys are lane keys: a branch, or kernel.TrunkLane.
//
// A version is the store's compare-and-swap token for one lane. A lane never
// saved has version 0 and the zero Record.
type Store interface {
	// Load returns the lane's record and its version. A lane the store has
	// never seen is not an error.
	Load(ctx context.Context, lane string) (Record, uint64, error)

	// Commit appends events to the log and saves rec as the lane's record, only
	// if the stored version is still expect, and returns the new version. The
	// event goes into the log before the checkpoint is replaced (§3 "Crash
	// safety"). When the version has moved it returns ErrConflict and changes
	// nothing: no event is appended, so a retry never logs a fact twice. A Commit
	// of no events saves the record alone.
	Commit(ctx context.Context, lane string, expect uint64, rec Record, events []kernel.Event) (uint64, error)
}
