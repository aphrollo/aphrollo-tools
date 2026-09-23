package tdd

import (
	"fmt"
	"time"
)

// unconstrainedGreen reports the case fail-first structurally cannot see: a
// SOURCE edit whose related tests all pass, with the same pass count as the
// last green for this project. No test came with the change, so nothing new
// constrains it — the gate has no evidence either way, which is exactly what
// a mutation proof is for. Advisory only.
func unconstrainedGreen(kind Kind, outcome Outcome, snap stateSnapshot, root string, passed int, hasCount bool) bool {
	if kind != Source || outcome != Green || !hasCount || snap.state == nil {
		return false
	}
	prev, ok := snap.state.ByProject[root]
	if !ok || prev.PassedCount == 0 {
		return false
	}
	return prev.PassedCount == passed
}

// unconstrainedLine is the one line that case prints.
func unconstrainedLine(r Runner, root string, passed int, dur time.Duration) string {
	return fmt.Sprintf("gate: %s in %s %s (%d passed; no test changed with this edit — mutation proof owed)",
		cmdString(r), root, GreenUnconstrained, passed)
}
