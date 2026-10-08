package suite

import (
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

// CostRecorder collects what the suites a stage ran cost, read out of the
// output they already produced: it adds no runner flag and no run. The merge
// gate stores the result as one aggregate; the commit gate reads it to tell
// how long a test it just ran took.
type CostRecorder struct {
	mu   sync.Mutex
	runs []testcost.Run
}

// NewCostRecorder is an empty recorder.
func NewCostRecorder() *CostRecorder { return &CostRecorder{} }

// Wrap is run, with each suite that passed and ran to its end noted. A red run
// stopped early and a timed-out one states a budget, so neither says what the
// suite costs.
func (c *CostRecorder) Wrap(run SuiteRunner) SuiteRunner {
	return func(r Runner, root string) SuiteResult {
		res := run(r, root)
		if res.Passed && !res.TimedOut {
			cost := testcost.ParseOutput(res.GoTestJSON, res.Output, res.Duration.Seconds())
			c.mu.Lock()
			c.runs = append(c.runs, cost)
			c.mu.Unlock()
		}
		return res
	}
}

// Run is the sum of what was noted, and false when no suite passed.
func (c *CostRecorder) Run() (testcost.Run, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.runs) == 0 {
		return testcost.Run{}, false
	}
	return testcost.Merge(c.runs...), true
}
