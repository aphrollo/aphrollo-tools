// Package costhistory reads the suite-cost record back out of the per-repo
// event log: the runs the merge gate wrote as suite.cost events, in the shape
// internal/testcost and the test_cost law work on. It is the one place the
// law's engine, which may not reach the gate's packages, learns the history.
package costhistory

import (
	"sort"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

// Runs is the suite.cost events among events as runs, oldest first. An event
// whose time does not parse is dropped: a run with no place in time cannot be
// windowed.
func Runs(events []core.Event) []testcost.Run {
	var out []testcost.Run
	for _, e := range events {
		if e.Kind != testcost.EventKind {
			continue
		}
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil {
			continue
		}
		out = append(out, testcost.FromDetail(e.Secs, at, e.Detail))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Read is the recorded runs of the repository root belongs to.
func Read(root string) []testcost.Run { return Runs(core.ReadEvents(root)) }
