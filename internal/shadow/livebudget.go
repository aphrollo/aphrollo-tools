package shadow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// How the live red→green wait is sized, as data. The floor is LiveBudget (50 ms,
// docs/trellis-architecture.md §4); a hook on a slow box waits for p90 of the times its
// decisions took, times BudgetHeadroom, up to BudgetCap: PreToolUse latency is felt on
// every write, so no history buys more than half a second. With fewer than
// BudgetMinSamples decisions on record there is no p90 worth trusting and the wait is
// the floor. Only the last BudgetWindow decisions count, so a box that got faster is
// sized by what it costs now.
const (
	BudgetCap        = 500 * time.Millisecond
	BudgetHeadroom   = 3
	BudgetMinSamples = 20
	BudgetWindow     = 64

	decisionsFile = "live-decisions.json"
)

// SizedBudget is the wait the recorded decision times ask for, between floor and
// BudgetCap. A floor above the cap (an explicit budget) is kept as it is.
func SizedBudget(floor time.Duration, samples []time.Duration) time.Duration {
	if len(samples) < BudgetMinSamples {
		return floor
	}
	s := slices.Clone(samples)
	slices.Sort(s)
	p90 := s[(len(s)*9+9)/10-1] // nearest rank
	return min(max(p90*BudgetHeadroom, floor), max(BudgetCap, floor))
}

// readDecisions is the decision times recorded in dir, oldest first; none when there is no
// dir or no readable record. The file is a hint: a missing or torn one costs the floor.
func readDecisions(dir string) []time.Duration {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, decisionsFile))
	if err != nil {
		return nil
	}
	var us []int64
	if json.Unmarshal(data, &us) != nil {
		return nil
	}
	out := make([]time.Duration, len(us))
	for i, u := range us {
		out[i] = time.Duration(u) * time.Microsecond
	}
	return out
}

// noteDecision appends the time one decision took (for a dropped one, the budget it hit:
// it took at least that) to the record in dir, keeping the last BudgetWindow. It is
// best-effort: two hooks that write at once lose one sample, and a failed write loses it.
func noteDecision(dir string, d time.Duration) {
	if dir == "" {
		return
	}
	h := append(readDecisions(dir), d)
	if len(h) > BudgetWindow {
		h = h[len(h)-BudgetWindow:]
	}
	us := make([]int64, len(h))
	for i, v := range h {
		us[i] = v.Microseconds()
	}
	data, err := json.Marshal(us)
	if err != nil {
		return
	}
	_ = core.WriteFileAtomic(filepath.Join(dir, decisionsFile), data)
}
