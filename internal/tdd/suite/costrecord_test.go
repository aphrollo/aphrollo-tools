package suite

import (
	"reflect"
	"testing"
	"time"
)

const costRecorderStream = `{"Action":"pass","Package":"m/a","Test":"TestSlow","Elapsed":12.5}
{"Action":"pass","Package":"m/a","Elapsed":13}
`

func costRecorderFake(res SuiteResult) SuiteRunner {
	return func(Runner, string) SuiteResult { return res }
}

// A merge gate that ran two suites (a Go one and a split second run) owes one
// cost for the merge: the seconds add up and the named tests are kept.
func TestCostRecorder_SumsThePassedRunsItWrapped(t *testing.T) {
	rec := NewCostRecorder()
	a := rec.Wrap(costRecorderFake(SuiteResult{Passed: true, Duration: 90 * time.Second, GoTestJSON: costRecorderStream}))
	b := rec.Wrap(costRecorderFake(SuiteResult{Passed: true, Duration: 30 * time.Second}))
	a(Runner{}, "")
	b(Runner{}, "")

	got, ok := rec.Run()
	if !ok {
		t.Fatal("no run recorded after two passed suites")
	}
	if got.Secs != 120 {
		t.Errorf("secs = %v, want 120", got.Secs)
	}
	if want := map[string]float64{"m/a.TestSlow": 12.5}; !reflect.DeepEqual(got.Tests, want) {
		t.Errorf("tests = %v, want %v", got.Tests, want)
	}
}

// A red or timed-out suite measures nothing about what the suite costs: a
// timeout is a budget, a failure stopped early.
func TestCostRecorder_IgnoresARunThatDidNotPass(t *testing.T) {
	rec := NewCostRecorder()
	for _, res := range []SuiteResult{
		{Passed: false, Duration: time.Minute},
		{Passed: true, TimedOut: true, Duration: time.Minute},
	} {
		rec.Wrap(costRecorderFake(res))(Runner{}, "")
	}
	if _, ok := rec.Run(); ok {
		t.Fatal("a run that did not pass was recorded as the suite's cost")
	}
}

func TestCostRecorder_WrapPassesTheResultThroughUnchanged(t *testing.T) {
	want := SuiteResult{Passed: true, Output: "out", Duration: time.Second}
	got := NewCostRecorder().Wrap(costRecorderFake(want))(Runner{}, "")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
}
