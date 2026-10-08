package postedit

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

// unconstrainedLine is the one line that case prints. cached is how many of
// the packages go served from its test cache; it is named so a cached green is
// never read as a fresh run.
func unconstrainedLine(r Runner, root string, passed, cached int, dur time.Duration) string {
	count := fmt.Sprintf("%d passed", passed)
	if cached > 0 {
		count = fmt.Sprintf("%d passed (%d cached)", passed, cached)
	}
	return withTargetsNotRun(fmt.Sprintf("gate: %s in %s %s (%s; no test changed with this edit — mutation proof owed)",
		cmdString(r), root, GreenUnconstrained, count), r, root)
}
