package engine

import (
	"fmt"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
	"pgregory.net/rapid"
)

var (
	propHooks = []render.Hook{
		render.HookPreToolUse, render.HookPostToolUse, render.HookPostToolBatch, render.HookSubagentStart,
		render.HookUserPromptSubmit, render.HookSessionStart, render.HookStop, render.HookSubagentStop,
		render.HookSessionEnd, render.HookTaskCompleted, render.HookCwdChanged, render.HookDirectoryAdded,
		render.HookSetup, "FromTheFuture",
	}
	propActors = []string{"s1/", "s1/a26c", "s2/"}
)

// linePool is n distinct lines: reds and greens of different units and trees.
func linePool(n int) []render.Line {
	var pool []render.Line
	for i := range n {
		unit, tree := fmt.Sprintf("pkg/u%d", i%5), fmt.Sprintf("t%d", i)
		if i%2 == 0 {
			pool = append(pool, redFor(unit, tree))
		} else {
			pool = append(pool, greenFor(unit, tree))
		}
	}
	return pool
}

func drawLines(t *rapid.T, pool []render.Line, label string) []render.Line {
	idx := rapid.SliceOfN(rapid.IntRange(0, len(pool)-1), 0, len(pool)).Draw(t, label)
	var out []render.Line
	for _, i := range idx {
		out = append(out, pool[i])
	}
	return out
}

// TestUnseen_isExactlyWhatNoReachingDeliveryCovered holds the seen rule over
// any history of deliveries below the bounds: a line is withheld from an actor
// when a delivery of it, through a hook that reaches the agent, went to that
// actor, and from nobody else. The model is a plain map the test keeps; no
// delivery of another line, however late, hides an unseen one.
func TestUnseen_isExactlyWhatNoReachingDeliveryCovered(t *testing.T) {
	pool := linePool(6)
	rapid.Check(t, func(rt *rapid.T) {
		eng, _ := newEngine(kernel.Config{})
		seen := map[string]map[string]bool{}
		steps := rapid.IntRange(1, 25).Draw(rt, "steps")
		for i := range steps {
			actor := rapid.SampledFrom(propActors).Draw(rt, "actor")
			hook := rapid.SampledFrom(propHooks).Draw(rt, "hook")
			lines := drawLines(rt, pool, "delivered")
			if err := eng.Deliver(bounded(t), "fix", actor, hook, lines); err != nil {
				rt.Fatalf("step %d: Deliver: %v", i, err)
			}
			if seen[actor] == nil {
				seen[actor] = map[string]bool{}
			}
			for _, l := range lines {
				if reaches(hook) {
					seen[actor][l.ID] = true
				}
			}

			asked := rapid.SampledFrom(propActors).Draw(rt, "asked")
			batch := drawLines(rt, pool, "batch")
			got, err := eng.Unseen(bounded(t), "fix", asked, batch)
			if err != nil {
				rt.Fatalf("step %d: Unseen: %v", i, err)
			}
			var want []render.Line
			taken := map[string]bool{}
			for _, l := range batch {
				if !seen[asked][l.ID] && !taken[l.ID] {
					want = append(want, l)
					taken[l.ID] = true
				}
			}
			if !sameLines(got, want) {
				rt.Fatalf("step %d: Unseen for %q = %v, want %v (seen: %v)", i, asked, ids(got), ids(want), seen[asked])
			}
		}
	})
}

// reaches is the F3 result the property relies on, stated again here so a
// change to render.Reaches that drops or adds a hook shows up in this test.
func reaches(h render.Hook) bool {
	return h == render.HookPostToolBatch || h == render.HookSubagentStart
}

// TestDeliver_boundsHoldAndEvictionOnlyRepeats runs more lines and actors past
// the bounds than they keep. The record never grows past them, the line a
// delivery put last is always kept, and a line the record forgot is due again:
// the bound can repeat a line, never hide one.
func TestDeliver_boundsHoldAndEvictionOnlyRepeats(t *testing.T) {
	pool := linePool(MaxSeenPerActor * 3)
	actors := make([]string, MaxSeenActors*2)
	for i := range actors {
		actors[i] = fmt.Sprintf("s%d/a", i)
	}
	rapid.Check(t, func(rt *rapid.T) {
		eng, store := newEngine(kernel.Config{})
		seen := map[string]map[string]bool{}
		for i := range rapid.IntRange(1, 60).Draw(rt, "steps") {
			actor := rapid.SampledFrom(actors).Draw(rt, "actor")
			lines := drawLines(rt, pool, "delivered")
			if err := eng.Deliver(bounded(t), "fix", actor, render.HookPostToolBatch, lines); err != nil {
				rt.Fatalf("step %d: Deliver: %v", i, err)
			}
			if seen[actor] == nil {
				seen[actor] = map[string]bool{}
			}
			for _, l := range lines {
				seen[actor][l.ID] = true
			}
			rec, _ := load(t, store, "fix")
			per := map[string]int{}
			for _, d := range rec.Delivered {
				per[d.Actor]++
			}
			if len(per) > MaxSeenActors {
				rt.Fatalf("step %d: deliveries kept for %d actors, bound %d", i, len(per), MaxSeenActors)
			}
			for a, n := range per {
				if n > MaxSeenPerActor {
					rt.Fatalf("step %d: %d deliveries kept for %q, bound %d", i, n, a, MaxSeenPerActor)
				}
			}
			if len(lines) > 0 {
				last := lines[len(lines)-1]
				if got, _ := eng.Unseen(bounded(t), "fix", actor, []render.Line{last}); len(got) != 0 {
					rt.Fatalf("step %d: the line just delivered to %q reads as unseen", i, actor)
				}
			}
			for _, a := range actors {
				got, err := eng.Unseen(bounded(t), "fix", a, pool)
				if err != nil {
					rt.Fatalf("step %d: Unseen: %v", i, err)
				}
				returned := map[string]bool{}
				for _, l := range got {
					returned[l.ID] = true
				}
				for _, l := range pool {
					if !seen[a][l.ID] && !returned[l.ID] {
						rt.Fatalf("step %d: %q never saw %v yet Unseen withheld it", i, a, l.ID)
					}
				}
			}
		}
	})
}
