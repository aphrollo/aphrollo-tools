package engine

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// MemStore is a Store in memory, for tests of the engine and of the adapters
// built on it. Like the real store it hands out copies: a record a caller
// changes after Load or Commit does not change the stored one.
type MemStore struct {
	mu     sync.Mutex
	lanes  map[string]memLane
	events map[string][]kernel.Event
}

type memLane struct {
	rec     Record
	version uint64
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{lanes: map[string]memLane{}, events: map[string][]kernel.Event{}}
}

// Load implements Store.
func (m *MemStore) Load(ctx context.Context, lane string) (Record, uint64, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.lanes[lane]
	return cloneRecord(l.rec), l.version, nil
}

// Commit implements Store.
func (m *MemStore) Commit(ctx context.Context, lane string, expect uint64, rec Record, events []kernel.Event) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.lanes[lane]
	if cur.version != expect {
		return 0, ErrConflict
	}
	m.events[lane] = append(m.events[lane], events...)
	m.lanes[lane] = memLane{rec: cloneRecord(rec), version: expect + 1}
	return expect + 1, nil
}

// Events returns a copy of the lane's log, oldest first.
func (m *MemStore) Events(lane string) []kernel.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.events[lane])
}

func cloneRecord(r Record) Record {
	r.Lane.Actors = maps.Clone(r.Lane.Actors)
	r.Lane.CI = maps.Clone(r.Lane.CI)
	r.Lane.CIRequired = slices.Clone(r.Lane.CIRequired)
	r.Units = maps.Clone(r.Units)
	r.Guided = maps.Clone(r.Guided)
	r.Delivered = slices.Clone(r.Delivered)
	return r
}
