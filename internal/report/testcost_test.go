package report

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func costEvent(seq int64, ageMin float64, secs float64, tests ...string) tdd.Event {
	e := evAt(seq, ageMin, "suite.cost", "lane/a", "", tests...)
	e.Secs = secs
	return e
}

func stageEvent(seq int64, kind string, secs float64, kv ...string) tdd.Event {
	e := evAt(seq, 30, kind, "lane/a", "green", kv...)
	e.Secs = secs
	return e
}

func costFixtureEvents() []tdd.Event {
	return []tdd.Event{
		costEvent(1, 3000, 400, "t:m.A", "10"),
		costEvent(2, 2000, 420, "t:m.A", "14", "t:m.B", "20"),
		costEvent(3, 1000, 410, "t:m.A", "13", "t:m.B", "21"),
		stageEvent(4, "commit_gate_result", 90),
		stageEvent(5, "merge_gate_result", 180),
		evAt(6, 20, "ci", "lane/a", "green", "secs", "600", "pr", "7"),
	}
}

func TestBuild_TestCostIsTheMedianSuiteAndTheSlowestTestsWithTheirTrend(t *testing.T) {
	r := build(costFixtureEvents())
	c := r.TestCost
	if c.Merges != 3 || c.SuiteP50 != 410 {
		t.Fatalf("merges %d, suite p50 %v; want 3 and 410", c.Merges, c.SuiteP50)
	}
	want := []CostTest{{Test: "m.B", Secs: 21, Delta: 1}, {Test: "m.A", Secs: 13, Delta: -1}}
	if len(c.Slowest) != 2 || c.Slowest[0] != want[0] || c.Slowest[1] != want[1] {
		t.Fatalf("slowest = %+v, want %+v (m.B was 20s the run before, m.A 14s)", c.Slowest, want)
	}
}

func TestBuild_TestCostMergeTimeByStageNamesTheLocalGateCIAndTheQueue(t *testing.T) {
	c := build(costFixtureEvents()).TestCost
	got := map[string]float64{}
	for _, s := range c.Stages {
		got[s.Stage] = s.P50
	}
	for stage, want := range map[string]float64{"local commit gate": 90, "local merge gate": 180, "CI": 600} {
		if got[stage] != want {
			t.Errorf("stage %q p50 = %v, want %v (stages %+v)", stage, got[stage], want, c.Stages)
		}
	}
	if c.LocalOnly {
		t.Error("the stages were labelled local only though CI time is in the log")
	}
}

func TestBuild_TestCostLabelsLocalStagesOnlyWhenTheLogHasNoCIOrQueueTime(t *testing.T) {
	c := build([]tdd.Event{stageEvent(1, "merge_gate_result", 180)}).TestCost
	if !c.LocalOnly || len(c.Stages) != 1 {
		t.Fatalf("stages %+v local-only %v; want the one local stage, labelled", c.Stages, c.LocalOnly)
	}
	if !strings.Contains(c.Text(), "local stages only") {
		t.Errorf("the text does not say so:\n%s", c.Text())
	}
}

func TestBuild_TestCostWithNothingRecordedSaysSoInsteadOfPrintingAnEmptyTable(t *testing.T) {
	c := build(nil).TestCost
	if c.Merges != 0 || len(c.Slowest) != 0 {
		t.Fatalf("test cost of an empty log = %+v", c)
	}
	if !strings.Contains(c.Text(), "no merge-gate suite cost recorded") {
		t.Errorf("text = %q", c.Text())
	}
}

func TestBuild_TestCostLeavesOutTheRunsBeforeTheWindow(t *testing.T) {
	old := costEvent(1, 60*24*20, 9000, "t:m.Old", "99")
	c := build(append(costFixtureEvents(), old)).TestCost
	if c.Merges != 3 {
		t.Fatalf("merges = %d, want the 3 in the 7d window", c.Merges)
	}
	for _, s := range c.Slowest {
		if s.Test == "m.Old" {
			t.Fatal("a test from before the window is in the slowest list")
		}
	}
}

func TestBuildTestCost_IsTheSameSectionForStats(t *testing.T) {
	got := BuildTestCost(costFixtureEvents(), now, week)
	if got.Merges != 3 {
		t.Fatalf("merges = %d, want 3", got.Merges)
	}
}
