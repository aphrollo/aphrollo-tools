package suite

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRuns stands in for the suite runner: it records every command it is
// handed and answers from a script, so a plan's executor is proven without a
// process, a clock or a box.
type fakeRuns struct {
	mu          sync.Mutex
	calls       []Runner
	inFlight    int
	maxInFlight int
	answer      func(call int, r Runner) SuiteResult
	// hold, when set, is called while the run counts as in flight.
	hold func(call int)
}

func (f *fakeRuns) run(r Runner, _ string) SuiteResult {
	f.mu.Lock()
	call := len(f.calls)
	f.calls = append(f.calls, r)
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	f.mu.Unlock()
	if f.hold != nil {
		f.hold(call)
	}
	res := f.answer(call, r)
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return res
}

// pkgsOf is the package patterns a command names.
func pkgsOf(r Runner) []string {
	var out []string
	for _, a := range r.Args {
		if strings.HasPrefix(a, "./") {
			out = append(out, a)
		}
	}
	return out
}

// passEvents is the `go test -json` lines a green run of pkgs leaves, each
// package at secs seconds.
func passEvents(secs float64, pkgs ...string) string {
	var b strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&b, `{"Action":"pass","Package":"%s/%s","Elapsed":%v}`+"\n", planModule, strings.TrimPrefix(p, "./"), secs)
	}
	return b.String()
}

func green(r Runner) SuiteResult {
	return SuiteResult{Passed: true, Output: "ok " + strings.Join(pkgsOf(r), " ") + "\n", GoTestJSON: passEvents(12.5, pkgsOf(r)...), Duration: 7 * time.Second}
}

// splitPlan is a plan over n unknown packages, run width wide.
func splitPlan(t *testing.T, n, width int) (GoTestPlan, Runner) {
	t.Helper()
	isolatedState(t)
	r := raceRunner(patterns(n)...)
	p := newGoTestPlan(r, planModule, nil, 600*time.Second)
	if !p.Split() {
		t.Fatalf("setup: %d unknown packages did not split", n)
	}
	p.parallel = width
	return p, r
}

// TestGoTestPlan_RunsEveryPackageOnceAndMergesTheOutputInPlanOrder pins the
// all-green path: five runs go one after another, between them they name
// each of the twenty packages once with the flags the line always had, and
// the verdict is the one green result with every run's output in plan order.
func TestGoTestPlan_RunsEveryPackageOnceAndMergesTheOutputInPlanOrder(t *testing.T) {
	p, r := splitPlan(t, 20, 1)
	f := &fakeRuns{answer: func(_ int, r Runner) SuiteResult { return green(r) }}

	got := p.Wrap(f.run)(r, "root")

	if len(f.calls) != 5 {
		t.Fatalf("%d runs, want 5", len(f.calls))
	}
	var seen, wantOut []string
	for _, c := range f.calls {
		seen = append(seen, pkgsOf(c)...)
		wantOut = append(wantOut, "ok "+strings.Join(pkgsOf(c), " ")+"\n")
		if !slices.Equal(c.Args[:4], []string{"test", "-race", "-count=1", "-shuffle=on"}) {
			t.Errorf("run %v lost its flags: %v", pkgsOf(c), c.Args)
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, patterns(20)) {
		t.Errorf("packages across runs = %v, want the twenty given once each", seen)
	}
	if !got.Passed || got.TimedOut || got.Output != strings.Join(wantOut, "") {
		t.Fatalf("merged = passed %v timedOut %v output %q, want one green result with the runs' outputs in order", got.Passed, got.TimedOut, got.Output)
	}
	if n := strings.Count(got.GoTestJSON, `"Action":"pass"`); n != 20 {
		t.Errorf("merged JSON carries %d package passes, want 20 (the vacuous-run check reads it)", n)
	}
}

// TestGoTestPlan_RunsSideBySideNoWiderThanTheWidth pins the bound: with two
// runs in flight together at most, four runs all complete, and the two do
// overlap (a width that never runs two at once would be sequential by accident).
func TestGoTestPlan_RunsSideBySideNoWiderThanTheWidth(t *testing.T) {
	p, r := splitPlan(t, 20, 2)
	var barrier sync.WaitGroup
	barrier.Add(2)
	met := make(chan struct{})
	go func() { barrier.Wait(); close(met) }()
	f := &fakeRuns{answer: func(_ int, r Runner) SuiteResult { return green(r) }}
	f.hold = func(call int) {
		if call < 2 {
			barrier.Done()
			select {
			case <-met:
			case <-time.After(10 * time.Second):
			}
		}
	}

	got := p.Wrap(f.run)(r, "root")

	if !got.Passed || len(f.calls) != 5 {
		t.Fatalf("passed %v after %d runs, want green after 5", got.Passed, len(f.calls))
	}
	if f.maxInFlight != 2 {
		t.Fatalf("most runs in flight = %d, want exactly 2: the first two must overlap and never three", f.maxInFlight)
	}
}

// TestGoTestPlan_ARedRunIsTheVerdictAndNoLaterRunStarts pins fail-fast: the
// second of four runs fails, so the verdict is that failure (red, not a
// timeout), its output is kept, and the third and fourth never start.
func TestGoTestPlan_ARedRunIsTheVerdictAndNoLaterRunStarts(t *testing.T) {
	p, r := splitPlan(t, 20, 1)
	f := &fakeRuns{answer: func(call int, r Runner) SuiteResult {
		if call == 1 {
			return SuiteResult{Passed: false, Output: "--- FAIL: TestBroken\nFAIL\n", Err: "exit status 1", Duration: 3 * time.Second}
		}
		return green(r)
	}}

	got := p.Wrap(f.run)(r, "root")

	if len(f.calls) != 2 {
		t.Fatalf("%d runs, want the run to stop after the failing second", len(f.calls))
	}
	if got.Passed || got.TimedOut || !strings.Contains(got.Output, "--- FAIL: TestBroken") || got.Err != "exit status 1" {
		t.Fatalf("merged = passed %v timedOut %v err %q output %q, want red with the failure's output", got.Passed, got.TimedOut, got.Err, got.Output)
	}
}

// TestGoTestPlan_ATimedOutRunNamesItsPackagesAndIsNeverAPass pins the
// inconclusive verdict: the second run times out, so the result is a timeout
// that names that run's packages and the runs that never started, the first
// run's green output is kept, and nothing about it reads as a pass.
func TestGoTestPlan_ATimedOutRunNamesItsPackagesAndIsNeverAPass(t *testing.T) {
	p, r := splitPlan(t, 20, 1)
	f := &fakeRuns{answer: func(call int, r Runner) SuiteResult {
		if call == 1 {
			return SuiteResult{TimedOut: true, Output: "partial\n", Duration: 600 * time.Second}
		}
		return green(r)
	}}

	got := p.Wrap(f.run)(r, "root")

	if got.Passed || !got.TimedOut || got.Inconclusive != "" {
		t.Fatalf("merged = passed %v timedOut %v inconclusive %q, want a plain timeout", got.Passed, got.TimedOut, got.Inconclusive)
	}
	second := "[" + strings.Join(names(p.groups[1]), " ") + "]"
	if !strings.Contains(got.SplitNote, "run 2 of 5 "+second+" after 600s") {
		t.Errorf("note = %q, want it to name run 2 with its packages and the 600s it ran", got.SplitNote)
	}
	third := "[" + strings.Join(names(p.groups[2]), " ") + "]"
	if !strings.Contains(got.SplitNote, "not started: run 3 of 5 "+third) {
		t.Errorf("note = %q, want it to name the runs that never started", got.SplitNote)
	}
	if !strings.HasPrefix(got.Output, "ok "+strings.Join(names(p.groups[0]), " ")+"\n") || !strings.Contains(got.Output, "partial\n") {
		t.Errorf("output = %q, want the first run's green output and the timed-out run's partial output", got.Output)
	}
}

// TestGoTestPlan_ARedRunBesideATimedOutRunReadsAsRed pins the precedence: a
// real failure is reported as the failure it is, never hidden behind a
// timeout of its neighbour.
func TestGoTestPlan_ARedRunBesideATimedOutRunReadsAsRed(t *testing.T) {
	p, r := splitPlan(t, 20, 2)
	var gate sync.WaitGroup
	gate.Add(2)
	met := make(chan struct{})
	go func() { gate.Wait(); close(met) }()
	f := &fakeRuns{
		hold: func(call int) {
			if call < 2 {
				gate.Done()
				select {
				case <-met:
				case <-time.After(10 * time.Second):
				}
			}
		},
		answer: func(call int, r Runner) SuiteResult {
			switch call {
			case 0:
				return SuiteResult{TimedOut: true, Duration: 600 * time.Second}
			case 1:
				return SuiteResult{Output: "--- FAIL: TestBroken\n", Duration: time.Second}
			}
			return green(r)
		},
	}

	got := p.Wrap(f.run)(r, "root")

	if got.Passed || got.TimedOut {
		t.Fatalf("merged = passed %v timedOut %v, want red: a failing run and a timed-out one together are a failure", got.Passed, got.TimedOut)
	}
}

// TestGoTestPlan_AnEndedRunKeepsItsOwnReasonAndIsNotATimeout pins the
// inconclusive-for-another-reason case: a run the memory cap ended carries its
// reason up, unfinished, and is not worded as a slow suite.
func TestGoTestPlan_AnEndedRunKeepsItsOwnReasonAndIsNotATimeout(t *testing.T) {
	p, r := splitPlan(t, 20, 1)
	f := &fakeRuns{answer: func(call int, r Runner) SuiteResult {
		if call == 0 {
			return SuiteResult{TimedOut: true, Inconclusive: "OOM-KILLED at 11.6 GB", Duration: 90 * time.Second}
		}
		return green(r)
	}}

	got := p.Wrap(f.run)(r, "root")

	if got.Passed || !got.TimedOut || !strings.Contains(got.Inconclusive, "OOM-KILLED at 11.6 GB") {
		t.Fatalf("merged = passed %v timedOut %v inconclusive %q, want unfinished with the cap's reason", got.Passed, got.TimedOut, got.Inconclusive)
	}
}

// TestGoTestPlan_EachRunHasItsOwnBudgetInsideTheOverallDeadline pins the
// clocks: every run is given the per-run budget from when it starts, and no
// run is ever given more than the deadline the whole stage has left.
func TestGoTestPlan_EachRunHasItsOwnBudgetInsideTheOverallDeadline(t *testing.T) {
	p, r := splitPlan(t, 20, 1)
	f := &fakeRuns{answer: func(_ int, r Runner) SuiteResult { return green(r) }}

	r.Deadline = time.Now().Add(30 * time.Minute)
	before := time.Now()
	p.Wrap(f.run)(r, "root")
	for i, c := range f.calls {
		if lo, hi := before.Add(600*time.Second), time.Now().Add(600*time.Second); c.Deadline.Before(lo) || c.Deadline.After(hi) {
			t.Errorf("run %d deadline %v, want the 600s per-run budget from its start (%v..%v)", i, c.Deadline, lo, hi)
		}
	}

	f.calls = nil
	r.Deadline = time.Now().Add(100 * time.Second)
	p.Wrap(f.run)(r, "root")
	for i, c := range f.calls {
		if !c.Deadline.Equal(r.Deadline) {
			t.Errorf("run %d deadline %v, want the stage's own %v: no run outlives the overall budget", i, c.Deadline, r.Deadline)
		}
	}
}

// TestGoTestPlan_NoRunStartsOnceTheOverallBudgetIsSpent pins the cap: with the
// stage's deadline already gone, nothing is started, and the result is a
// timeout naming every run, not a pass over zero tests.
func TestGoTestPlan_NoRunStartsOnceTheOverallBudgetIsSpent(t *testing.T) {
	p, r := splitPlan(t, 20, 2)
	f := &fakeRuns{answer: func(_ int, r Runner) SuiteResult { return green(r) }}
	r.Deadline = time.Now().Add(-time.Second)

	got := p.Wrap(f.run)(r, "root")

	if len(f.calls) != 0 {
		t.Fatalf("%d runs started past the overall budget, want none", len(f.calls))
	}
	if got.Passed || !got.TimedOut || !strings.Contains(got.SplitNote, "not started: run 1 of 5") {
		t.Fatalf("merged = passed %v timedOut %v note %q, want a timeout naming the runs that never started", got.Passed, got.TimedOut, got.SplitNote)
	}
}

// TestGoTestPlan_RecordsEachFinishedPackageAndCutOffOnesOfATimedOutRun pins
// what the next plan learns: every package a run's JSON shows passed is
// recorded at its own seconds, and a package of a run that timed out without
// finishing is recorded as cut off at the seconds that run had spent.
func TestGoTestPlan_RecordsEachFinishedPackageAndCutOffOnesOfATimedOutRun(t *testing.T) {
	p, r := splitPlan(t, 20, 1)
	timedOut := names(p.groups[1])
	f := &fakeRuns{answer: func(call int, r Runner) SuiteResult {
		if call == 1 {
			// One of its packages finished before the cut.
			return SuiteResult{TimedOut: true, GoTestJSON: passEvents(40, timedOut[0]), Duration: 600 * time.Second}
		}
		return green(r)
	}}

	p.Wrap(f.run)(r, "root")

	est := recordedPkgSecs(true)
	if got := est[planModule+"/"+strings.TrimPrefix(names(p.groups[0])[0], "./")]; got != 12.5 {
		t.Errorf("estimate for a package of a green run = %v, want 12.5", got)
	}
	if got := est[importPathOf(planModule, timedOut[0])]; got != 40 {
		t.Errorf("estimate for the package that finished in the timed-out run = %v, want its own 40s", got)
	}
	if got := est[importPathOf(planModule, timedOut[1])]; got != 600 {
		t.Errorf("estimate for a package the timed-out run left unfinished = %v, want the 600s it was cut off at", got)
	}
	if _, ok := est[importPathOf(planModule, names(p.groups[2])[0])]; ok {
		t.Errorf("a package of a run that never started has an estimate, want none")
	}
}

// TestGoTestPlan_ARunTheMemoryCapEndedRecordsNothingForItsUnfinishedPackages
// pins that only a slow run is evidence of cost: a run the cap ended says
// nothing about how long its packages take.
func TestGoTestPlan_ARunTheMemoryCapEndedRecordsNothingForItsUnfinishedPackages(t *testing.T) {
	p, r := splitPlan(t, 20, 1)
	f := &fakeRuns{answer: func(_ int, _ Runner) SuiteResult {
		return SuiteResult{TimedOut: true, Inconclusive: "OOM-KILLED at 11.6 GB", Duration: 90 * time.Second}
	}}

	p.Wrap(f.run)(r, "root")

	if est := recordedPkgSecs(true); len(est) != 0 {
		t.Fatalf("estimates after a run the cap ended = %v, want none", est)
	}
}

// TestGoTestPlan_AFittingRunStaysOneCommandAndStillRecords pins the other
// half: a list that fits is run exactly as asked, and its packages are
// recorded, which is how a plan ever learns anything for a list that fits.
func TestGoTestPlan_AFittingRunStaysOneCommandAndStillRecords(t *testing.T) {
	isolatedState(t)
	r := raceRunner(patterns(3)...)
	p := newGoTestPlan(r, planModule, nil, 600*time.Second)
	if p.Split() {
		t.Fatalf("setup: 3 unknown packages split")
	}
	f := &fakeRuns{answer: func(_ int, r Runner) SuiteResult { return green(r) }}

	got := p.Wrap(f.run)(r, "root")

	if len(f.calls) != 1 || !slices.Equal(f.calls[0].Args, r.Args) {
		t.Fatalf("calls = %v, want the one command exactly as asked", f.calls)
	}
	if !got.Passed {
		t.Fatalf("merged = %+v, want the run's own result", got)
	}
	if est := recordedPkgSecs(true); len(est) != 3 || est[planModule+"/p01"] != 12.5 {
		t.Fatalf("estimates = %v, want the three packages at 12.5s", est)
	}
}

// TestGoTestPlan_ALineItCannotRebuildPassesThroughUnrecorded pins that a
// filtered run is handed straight on and teaches the record nothing.
func TestGoTestPlan_ALineItCannotRebuildPassesThroughUnrecorded(t *testing.T) {
	isolatedState(t)
	r := Runner{Cmd: "go", Args: []string{"test", "-run", "TestX", "./p00", "./p01"}}
	p := newGoTestPlan(r, planModule, nil, 600*time.Second)
	f := &fakeRuns{answer: func(_ int, r Runner) SuiteResult { return green(r) }}

	p.Wrap(f.run)(r, "root")

	if len(f.calls) != 1 {
		t.Fatalf("%d runs, want 1", len(f.calls))
	}
	if est := recordedPkgSecs(true); len(est) != 0 {
		t.Fatalf("a filtered run recorded %v, want nothing", est)
	}
}

// TestGoTestPlan_ARealGoTestIsCutIntoTwoRunsAndRecordsBothPackages is the
// end-to-end proof on the real toolchain: two tiny packages recorded at 100s
// each (150s weighed, so two do not fit a 170s run) are run as two real `go
// test` commands, each package once, the verdict is the one green result, and
// the seconds each package really took are in the record afterwards.
func TestGoTestPlan_ARealGoTestIsCutIntoTwoRunsAndRecordsBothPackages(t *testing.T) {
	isolatedState(t)
	dir := makeGoRepo(t)
	module := modulePathOf(dir)
	if module == "" {
		t.Fatalf("setup: the fixture module has no module line")
	}
	for _, pkg := range []string{"a", "b"} {
		write(t, dir, pkg+"/"+pkg+"_test.go", "package "+pkg+"\n\nimport \"testing\"\n\nfunc TestAddition(t *testing.T) {\n\tif 1+1 != 2 {\n\t\tt.Fatal(\"arithmetic\")\n\t}\n}\n")
	}
	a, b := sampleAt(module+"/a", 100, time.Hour), sampleAt(module+"/b", 100, time.Hour)
	a.Race, b.Race = false, false
	recordPkgSamples([]pkgSample{a, b})
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "-shuffle=on", "./a", "./b"}}
	p := PlanGoTestRun(r, dir, 170*time.Second)
	if !p.Split() || len(p.groups) != 2 {
		t.Fatalf("setup: plan has %d runs, want the two packages cut into two", len(p.groups))
	}
	var runs atomic.Int64
	real := RunSuite(3 * time.Minute)
	counting := func(r Runner, root string) SuiteResult {
		runs.Add(1)
		return real(r, root)
	}

	got := p.Wrap(counting)(r, dir)

	if !got.Passed || got.TimedOut {
		t.Fatalf("merged = passed %v timedOut %v err %q output %q, want green", got.Passed, got.TimedOut, got.Err, got.Output)
	}
	if n := runs.Load(); n != 2 {
		t.Fatalf("%d real go test runs, want 2", n)
	}
	for _, pkg := range []string{"a", "b"} {
		if !strings.Contains(got.GoTestJSON, `"Package":"`+module+"/"+pkg+`"`) {
			t.Errorf("merged JSON has no event for package %s", pkg)
		}
		seen := false
		for _, s := range readPkgSamples(pkgSecsPath()) {
			if s.Pkg == module+"/"+pkg && !s.Cut && s.Secs > 0 && s.Secs < 50 {
				seen = true
			}
		}
		if !seen {
			t.Errorf("no recorded run of package %s, want the seconds it really took", pkg)
		}
	}
}

// TestSplitNote_NamesOnlyWhatTheRunLeftUndoneInTheOrderItIsWorded pins the
// words a refusal carries: a part appears only for a run that has something to
// say (the unfinished, the ended, the unstarted), never as an empty heading,
// and the parts are one per line.
func TestSplitNote_NamesOnlyWhatTheRunLeftUndoneInTheOrderItIsWorded(t *testing.T) {
	cases := []struct {
		name                       string
		unfinished, ended, unstart []string
		want                       string
	}{
		{"nothing", nil, nil, nil, ""},
		{"unfinished only", []string{"run 1 of 3 [a] after 600s"}, nil, nil,
			"1 of 3 runs did not finish: run 1 of 3 [a] after 600s"},
		{"ended only", nil, []string{"run 2 of 3 [b] ended: memory cap", "run 3 of 3 [c] ended: memory cap"}, nil,
			"run 2 of 3 [b] ended: memory cap; run 3 of 3 [c] ended: memory cap"},
		{"unstarted only", nil, nil, []string{"run 2 of 3 [b]", "run 3 of 3 [c]"},
			"not started: run 2 of 3 [b], run 3 of 3 [c]"},
		{"all three", []string{"run 1 of 3 [a] after 600s"}, []string{"run 2 of 3 [b] ended: memory cap"}, []string{"run 3 of 3 [c]"},
			"1 of 3 runs did not finish: run 1 of 3 [a] after 600s\nrun 2 of 3 [b] ended: memory cap\nnot started: run 3 of 3 [c]"},
	}
	for _, tc := range cases {
		if got := splitNote(3, tc.unfinished, tc.ended, tc.unstart); got != tc.want {
			t.Errorf("%s: splitNote = %q, want %q", tc.name, got, tc.want)
		}
	}
}
