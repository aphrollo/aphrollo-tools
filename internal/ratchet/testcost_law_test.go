package ratchet

import (
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

const testCostLawBody = `
name = "test_cost"
description = "the suite's cost at merge may only fall"
severity = "deny"
baseline = ".ratchet/baselines/test_cost.txt"

[scope]
include = [".ratchet/laws/test_cost.toml"]

[matcher]
kind = "test-cost"
window = 5
min_runs = 3
threshold_secs = 10
tolerance_pct = 10
`

// testCostRuns is one run per figure, oldest first; every run names `over`
// tests at 10s and one at 9s.
func testCostRuns(over int, secs ...float64) []testcost.Run {
	out := make([]testcost.Run, len(secs))
	for i, s := range secs {
		tests := map[string]float64{"m.Under": 9}
		for j := range over {
			tests["m.Slow"+strconv.Itoa(j)] = 10
		}
		out[i] = testcost.Run{At: time.Date(2026, 10, 1+i, 0, 0, 0, 0, time.UTC), Secs: s, Tests: tests}
	}
	return out
}

// withCostHistory is what the binary's own wiring does: the law reads the
// repo's recorded runs from here, never from the tree it judges.
func withCostHistory(t *testing.T, runs []testcost.Run) {
	t.Helper()
	old := CostHistory
	CostHistory = func(string) []testcost.Run { return runs }
	t.Cleanup(func() { CostHistory = old })
}

func testCostRepo(t *testing.T, baseline string) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "test_cost", testCostLawBody)
	if baseline != "" {
		write(t, filepath.Join(root, ".ratchet", "baselines", "test_cost.txt"), baseline)
	}
	return root
}

func TestParseLaw_TestCostFieldsAndDefaults(t *testing.T) {
	law, err := ParseLaw("name = \"c\"\ndescription = \"d\"\nseverity = \"deny\"\nbaseline = \"b.txt\"\n[scope]\ninclude = [\"a\"]\n[matcher]\nkind = \"test-cost\"\n", "c")
	if err != nil {
		t.Fatal(err)
	}
	m := law.Matcher
	if m.Cost.Window != 10 || m.Cost.MinRuns != 3 || m.Cost.ThresholdSecs != 10 {
		t.Fatalf("defaults = window %d, min_runs %d, threshold %d; want 10, 3, 10", m.Cost.Window, m.Cost.MinRuns, m.Cost.ThresholdSecs)
	}
	law, err = ParseLaw(testCostLawBody, "test_cost")
	if err != nil {
		t.Fatal(err)
	}
	m = law.Matcher
	if m.Cost.Window != 5 || m.Cost.MinRuns != 3 || m.Cost.ThresholdSecs != 10 || m.TolerancePct != 10 {
		t.Fatalf("fields = %+v", m)
	}
}

func TestParseLaw_TestCostRefusesANonPositiveWindow(t *testing.T) {
	_, err := ParseLaw("name = \"c\"\ndescription = \"d\"\nseverity = \"deny\"\nbaseline = \"b.txt\"\n[scope]\ninclude = [\"a\"]\n[matcher]\nkind = \"test-cost\"\nwindow = 0\n", "c")
	if err == nil {
		t.Fatal("window = 0 was accepted: a median over no runs is no measurement")
	}
}

func TestTestCostHits_AreTheSuiteMedianAndTheTestsOverTheThreshold(t *testing.T) {
	law, _ := ParseLaw(testCostLawBody, "test_cost")
	hits, err := testCostHits(testCostRuns(2, 100, 120, 110), law, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, h := range hits {
		got[h.Key] = h.Weight
	}
	want := map[string]int{"suite_secs_p50": 110, "tests_over_threshold": 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hits = %v, want %v (the 9s test is under a 10s threshold)", got, want)
	}
}

func TestTestCostHits_TooFewRunsAreNothingToJudgeUnlessAdopting(t *testing.T) {
	law, _ := ParseLaw(testCostLawBody, "test_cost")
	hits, err := testCostHits(testCostRuns(0, 100, 100), law, false)
	if err != nil || len(hits) != 0 {
		t.Fatalf("a check over two runs: hits %v, err %v; want neither", hits, err)
	}
	if _, err := testCostHits(testCostRuns(0, 100, 100), law, true); err == nil {
		t.Fatal("adopting a baseline from two runs was allowed: it would pin noise")
	}
}

func TestTestCost_OneSlowRunDoesNotFailTheLaw(t *testing.T) {
	root := testCostRepo(t, "suite_secs_p50 | 100\ntests_over_threshold | 0\n")
	withCostHistory(t, testCostRuns(0, 100, 101, 99, 100, 900))
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("one 900s run among five failed the law: %v", res.Lines())
	}
}

func TestTestCost_ASuiteThatStaysSlowFailsTheLawPastTheTolerance(t *testing.T) {
	root := testCostRepo(t, "suite_secs_p50 | 100\ntests_over_threshold | 0\n")
	withCostHistory(t, testCostRuns(0, 100, 120, 130, 120, 125))
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "suite_secs_p50" || res.Findings[0].Measured != 120 {
		t.Fatalf("findings = %v, want suite_secs_p50 at 120 against 100", res.Lines())
	}
}

func TestTestCost_TheToleranceEdgeIsAllowedAndOneSecondPastItIsNot(t *testing.T) {
	root := testCostRepo(t, "suite_secs_p50 | 100\ntests_over_threshold | 0\n")
	withCostHistory(t, testCostRuns(0, 110, 110, 110))
	if res, _ := Check(Options{Root: root}); len(res.Findings) != 0 {
		t.Fatalf("110s against 100s with 10%% tolerance: %v", res.Lines())
	}
	withCostHistory(t, testCostRuns(0, 111, 111, 111))
	if res, _ := Check(Options{Root: root}); len(res.Findings) != 1 {
		t.Fatalf("111s against 100s with 10%% tolerance was not a finding")
	}
}

func TestTestCost_ASlowTestAddedPastTheThresholdFailsTheLaw(t *testing.T) {
	root := testCostRepo(t, "suite_secs_p50 | 100\ntests_over_threshold | 1\n")
	withCostHistory(t, testCostRuns(2, 100, 100, 100))
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "tests_over_threshold" {
		t.Fatalf("findings = %v, want tests_over_threshold 2 against 1", res.Lines())
	}
}

func TestTestCost_TheBaselineTightensDownAndNeverUp(t *testing.T) {
	root := testCostRepo(t, "suite_secs_p50 | 100\ntests_over_threshold | 3\n")
	path := filepath.Join(root, ".ratchet", "baselines", "test_cost.txt")

	withCostHistory(t, testCostRuns(3, 105, 105, 105))
	if _, err := Check(Options{Root: root, Tighten: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "suite_secs_p50 | 100\ntests_over_threshold | 3\n" {
		t.Fatalf("a figure inside the tolerance moved the baseline: %q", got)
	}

	withCostHistory(t, testCostRuns(1, 80, 80, 80))
	if _, err := Check(Options{Root: root, Tighten: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "suite_secs_p50 | 80\ntests_over_threshold | 1\n" {
		t.Fatalf("baseline after a faster, leaner suite = %q, want it lowered to 80 and 1", got)
	}
}

// A box that has not recorded enough merges yet must not read as a suite that
// costs nothing: the baseline would be written down to zero.
func TestTestCost_TooLittleHistoryJudgesNothingAndLeavesTheBaselineAlone(t *testing.T) {
	root := testCostRepo(t, "suite_secs_p50 | 100\ntests_over_threshold | 3\n")
	path := filepath.Join(root, ".ratchet", "baselines", "test_cost.txt")
	withCostHistory(t, testCostRuns(0, 5, 5))
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings over two runs: %v", res.Lines())
	}
	if got := read(t, path); got != "suite_secs_p50 | 100\ntests_over_threshold | 3\n" {
		t.Fatalf("baseline after a run with no history = %q, want it untouched", got)
	}
}
