package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/costhistory"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

// costTopTests is how many of the slowest tests the section lists.
const costTopTests = 10

// CostStage is the median seconds of one stage of getting a change merged.
type CostStage struct {
	Stage string  `json:"stage"`
	P50   float64 `json:"p50_secs"`
	N     int     `json:"n"`
}

// CostTest is one of the slowest tests: its seconds in the newest merge-gate run
// that states it and the change since the run before; New when no earlier run in
// the window does.
type CostTest struct {
	Test  string  `json:"test"`
	Secs  float64 `json:"secs"`
	Delta float64 `json:"change_secs"`
	New   bool    `json:"new,omitempty"`
}

// TestCost is what the suite costs: the merge gate's suite runs the window
// recorded, the merge time by stage, and the slowest tests.
type TestCost struct {
	// Merges is the suite runs recorded in the window and SuiteP50 their median seconds.
	Merges   int     `json:"merges"`
	SuiteP50 float64 `json:"suite_p50_secs"`
	// Stages are the median seconds of the local commit gate, the local merge gate,
	// CI and the merge queue, those the window's log timed. LocalOnly is set when
	// neither CI nor the queue was.
	Stages    []CostStage `json:"stages"`
	LocalOnly bool        `json:"local_stages_only"`
	Slowest   []CostTest  `json:"slowest_tests"`
}

// costStages maps a Speed row to the stage the section names it by: data, so a
// new stage is a row here.
var costStages = []struct{ row, label string }{
	{"commit gate total", "local commit gate"},
	{"merge gate total", "local merge gate"},
	{"CI pipeline", "CI"},
	{"merge queue", "queue wait"},
}

// BuildTestCost is the section for the events of the window ending at now (the
// whole log when window is 0): what `aphrollo stats` prints beside the report's own.
func BuildTestCost(events []tdd.Event, now time.Time, window time.Duration) TestCost {
	var since time.Time
	if window > 0 {
		since = now.Add(-window)
	}
	return buildTestCost(sortEvents(events), since, buildSpeed(sortEvents(events), since, time.Time{}, false))
}

func buildTestCost(evs []stamped, since time.Time, speed Speed) TestCost {
	raw := make([]tdd.Event, len(evs))
	for i, e := range evs {
		raw[i] = e.Event
	}
	var runs []testcost.Run
	for _, r := range costhistory.Runs(raw) {
		if since.IsZero() || !r.At.Before(since) {
			runs = append(runs, r)
		}
	}
	c := TestCost{Merges: len(runs)}
	if p50, ok := testcost.SuiteP50(runs, 0, 1); ok {
		c.SuiteP50 = float64(p50)
	}
	for _, s := range testcost.Slowest(runs, costTopTests) {
		c.Slowest = append(c.Slowest, CostTest{Test: s.ID, Secs: s.Secs, Delta: s.Delta, New: s.New})
	}
	timed := false
	for _, st := range costStages {
		for _, row := range speed.Rows {
			if row.Stage != st.row {
				continue
			}
			c.Stages = append(c.Stages, CostStage{Stage: st.label, P50: row.P50, N: row.N})
			timed = timed || st.row == "CI pipeline" || st.row == "merge queue"
		}
	}
	c.LocalOnly = len(c.Stages) > 0 && !timed
	return c
}

func (c TestCost) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	if c.Merges == 0 {
		p("Test cost: no merge-gate suite cost recorded in the window (the merge gate writes one per merge it runs the suite for)")
	} else {
		p("Test cost (merge-gate suite runs recorded: %d, p50 %s)", c.Merges, secsText(c.SuiteP50))
	}
	if len(c.Stages) == 0 {
		p("  merge time by stage: none timed in the window")
	} else {
		parts := make([]string, len(c.Stages))
		for i, s := range c.Stages {
			parts[i] = fmt.Sprintf("%s %s", s.Stage, secsText(s.P50))
		}
		p("  merge time by stage (p50): %s", strings.Join(parts, ", "))
		if c.LocalOnly {
			p("  (local stages only: the window's log has no CI or queue time)")
		}
	}
	if len(c.Slowest) > 0 {
		p("  slowest tests (change against the run before; new = no earlier run in the window states it)")
		for _, s := range c.Slowest {
			trend := "new"
			if !s.New {
				trend = signedSecs(s.Delta)
			}
			p("    %-60.60s %-7s %s", s.Test, secsText(s.Secs), trend)
		}
	}
	return b.String()
}
