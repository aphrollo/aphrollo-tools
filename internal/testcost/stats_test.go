package testcost

import (
	"reflect"
	"testing"
	"time"
)

func runAt(day int, secs float64, tests map[string]float64) Run {
	return Run{At: time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC), Secs: secs, Tests: tests}
}

func TestDetail_RoundTripsKeepingTheSlowestAndDroppingTheFast(t *testing.T) {
	tests := map[string]float64{"m.TestSlow": 12.5, "m.TestQuick": 0.2}
	in := Run{Secs: 30, Tests: tests, Pkgs: map[string]float64{"m": 30.25}}
	got := FromDetail(in.Secs, in.At, in.Detail())
	want := Run{Secs: 30, Tests: map[string]float64{"m.TestSlow": 12.5}, Pkgs: map[string]float64{"m": 30.25}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, want %+v (a test under %.0fs is not worth a row)", got, want, MinRecordSecs)
	}
}

func TestDetail_KeepsAtMostMaxTests(t *testing.T) {
	tests := map[string]float64{}
	for i := range MaxTests + 10 {
		tests["m.Test"+string(rune('A'+i%26))+string(rune('a'+i/26))] = float64(2 + i)
	}
	got := FromDetail(1, time.Time{}, Run{Tests: tests}.Detail())
	if len(got.Tests) != MaxTests {
		t.Fatalf("kept %d tests, want %d", len(got.Tests), MaxTests)
	}
	if _, ok := got.Tests["m.TestAa"]; ok {
		t.Fatal("the fastest test was kept over a slower one")
	}
}

func TestSuiteP50_IsTheMedianOfTheLastNRuns(t *testing.T) {
	runs := []Run{runAt(1, 900, nil), runAt(2, 100, nil), runAt(3, 120, nil), runAt(4, 110, nil)}
	got, ok := SuiteP50(runs, 3, 3)
	if !ok || got != 110 {
		t.Fatalf("p50 = %v ok=%v, want 110 over the newest three (the day-1 outlier is outside the window)", got, ok)
	}
}

func TestSuiteP50_OneOutlierDoesNotMoveTheMedian(t *testing.T) {
	runs := []Run{runAt(1, 100, nil), runAt(2, 104, nil), runAt(3, 1000, nil), runAt(4, 98, nil), runAt(5, 101, nil)}
	got, _ := SuiteP50(runs, 5, 3)
	if got != 101 {
		t.Fatalf("p50 = %v, want 101: one slow run is not the suite's cost", got)
	}
}

func TestSuiteP50_FewerThanMinRunsIsNoJudgement(t *testing.T) {
	if _, ok := SuiteP50([]Run{runAt(1, 100, nil), runAt(2, 100, nil)}, 5, 3); ok {
		t.Fatal("two runs judged with a floor of three")
	}
}

func TestSuiteP50_RunsAreOrderedByTimeNotByArrival(t *testing.T) {
	runs := []Run{runAt(4, 110, nil), runAt(1, 900, nil), runAt(3, 120, nil), runAt(2, 100, nil)}
	got, _ := SuiteP50(runs, 3, 3)
	if got != 110 {
		t.Fatalf("p50 = %v, want 110: the window is the newest three by time", got)
	}
}

func TestOverP50_CountsTestsOverTheThresholdAtTheMedianRun(t *testing.T) {
	over := func(n int) map[string]float64 {
		m := map[string]float64{"m.Fast": 9.9}
		for i := range n {
			m["m.Slow"+string(rune('A'+i))] = 10
		}
		return m
	}
	runs := []Run{runAt(1, 1, over(1)), runAt(2, 1, over(7)), runAt(3, 1, over(2))}
	got, ok := OverP50(runs, 3, 3, 10)
	if !ok || got != 2 {
		t.Fatalf("over = %d ok=%v, want 2 (a test AT the threshold counts, 9.9s does not)", got, ok)
	}
}

func TestSlowest_RanksByLatestAndReportsTheTrend(t *testing.T) {
	runs := []Run{
		runAt(1, 1, map[string]float64{"m.A": 10, "m.B": 5}),
		runAt(2, 1, map[string]float64{"m.A": 14, "m.B": 5, "m.C": 20}),
	}
	got := Slowest(runs, 2)
	want := []Slow{
		{ID: "m.C", Secs: 20, New: true},
		{ID: "m.A", Secs: 14, Delta: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slowest = %+v, want %+v", got, want)
	}
}

func TestLookup_FindsATestByItsBareNameInTheNewestRunThatHasIt(t *testing.T) {
	runs := []Run{runAt(1, 1, map[string]float64{"m/a.TestX": 30}), runAt(2, 1, map[string]float64{"m/a.TestX": 12, "m/b.TestY": 3})}
	got, ok := Lookup(runs, "TestX")
	if !ok || got != 12 {
		t.Fatalf("lookup = %v ok=%v, want 12", got, ok)
	}
	if _, ok := Lookup(runs, "Test"); ok {
		t.Fatal("a prefix of a test name matched it")
	}
}
