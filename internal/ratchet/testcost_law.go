package ratchet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

// KindTestCost: what the suite costs at merge, read out of the per-repo history
// of recorded runs (internal/testcost) rather than out of the tree. Two
// figures, each of which may only fall: the median suite seconds and the median
// count of tests at or over a threshold, both over the newest runs. A median,
// and the law's tolerance, keep one slow run from failing anything.
const KindTestCost MatcherKind = "test-cost"

// TestCostParams are a test-cost law's fields: the newest Window recorded
// merges are judged, once there are MinRuns of them, and a test at or over
// ThresholdSecs seconds is slow. The tolerance is the matcher's TolerancePct.
type TestCostParams struct {
	Window        int
	MinRuns       int
	ThresholdSecs int
}

// The test-cost defaults: the newest ten merges, judged from the third, a test
// of ten seconds is slow.
const (
	defaultCostWindow    = 10
	defaultCostMinRuns   = 3
	defaultCostThreshold = 10
)

// The two figures' baseline keys.
const (
	costKeySuite = "suite_secs_p50"
	costKeyOver  = "tests_over_threshold"
)

// CostHistory is how a law reads the repo's recorded merges: the caller that
// owns the event log (the binary's main) sets it. nil, or a repo with no
// record, is no history, and a law with no history judges nothing.
var CostHistory func(root string) []testcost.Run

func costRunsOf(root string) []testcost.Run {
	if CostHistory == nil {
		return nil
	}
	return CostHistory(root)
}

// setTestCostFields reads the law's window, floor and threshold, each a
// positive integer, and its tolerance, which it shares with the ceilings.
func setTestCostFields(doc *tomlDoc, m *Matcher) error {
	for _, f := range []struct {
		key  string
		into *int
		def  int
	}{
		{"window", &m.Cost.Window, defaultCostWindow},
		{"min_runs", &m.Cost.MinRuns, defaultCostMinRuns},
		{"threshold_secs", &m.Cost.ThresholdSecs, defaultCostThreshold},
	} {
		*f.into = f.def
		if v, ok := doc.value("matcher", f.key); ok {
			if v.kind != tomlInt || v.i < 1 {
				return fmt.Errorf("matcher.%s is a positive integer", f.key)
			}
			*f.into = v.i
		}
	}
	if v, ok := doc.value("matcher", "tolerance_pct"); ok {
		if v.kind != tomlInt || v.i < 0 {
			return fmt.Errorf("matcher.tolerance_pct is a non-negative integer")
		}
		m.TolerancePct = v.i
	}
	return nil
}

// costUnjudgeable is a test-cost law over a repo that has not recorded enough
// merges yet. Checking and tightening are both skipped: a box with no history
// is not a suite that costs nothing, and a baseline written down to zero would
// be a ceiling no real run could meet.
func costUnjudgeable(root string, law Law) bool {
	if law.Matcher.Kind != KindTestCost {
		return false
	}
	_, enough := testcost.SuiteP50(costRunsOf(root), law.Matcher.Cost.Window, law.Matcher.Cost.MinRuns)
	return !enough
}

// testCostHits are the law's two figures over runs. requireData is adoption:
// a baseline written from too few runs would pin noise, so it is an error
// there, and quietly nothing in a check.
func testCostHits(runs []testcost.Run, law Law, requireData bool) ([]Hit, error) {
	c := law.Matcher.Cost
	p50, ok := testcost.SuiteP50(runs, c.Window, c.MinRuns)
	if !ok {
		if requireData {
			return nil, fmt.Errorf("law %q: %d recorded merge(s), fewer than min_runs = %d — a baseline from so few runs would pin noise; merge more first", law.Name, len(runs), c.MinRuns)
		}
		return nil, nil
	}
	over, _ := testcost.OverP50(runs, c.Window, c.MinRuns, float64(c.ThresholdSecs))
	return []Hit{
		{Law: law.Name, File: costKeySuite, Weight: p50, Key: costKeySuite,
			What: fmt.Sprintf("the suite's median is %ds over the newest %d recorded merges", p50, c.Window)},
		{Law: law.Name, File: costKeyOver, Weight: over, Key: costKeyOver,
			What: fmt.Sprintf("%d tests at or over %ds, the median of the newest %d recorded merges", over, c.ThresholdSecs, c.Window)},
	}, nil
}

// costFixtureFile is the recorded history a fixture directory stands in for,
// since a fixture is a tree and the real history is not in one: a JSON array of
// {"secs": n, "tests": {"name": secs}}, oldest first.
const costFixtureFile = "test-cost-runs.json"

// fixtureCostRuns reads a fixture's history; a missing or unreadable file is no
// history, which a test-cost law judges nothing over.
func fixtureCostRuns(dir string) []testcost.Run {
	data, err := os.ReadFile(filepath.Join(dir, costFixtureFile))
	if err != nil {
		return nil
	}
	var rows []struct {
		Secs  float64            `json:"secs"`
		Tests map[string]float64 `json:"tests"`
	}
	if json.Unmarshal(data, &rows) != nil {
		return nil
	}
	runs := make([]testcost.Run, len(rows))
	for i, r := range rows {
		runs[i] = testcost.Run{At: time.Unix(int64(i), 0), Secs: r.Secs, Tests: r.Tests}
	}
	return runs
}
