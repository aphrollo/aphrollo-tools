package mutation

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// selExecFake answers the `go test` runs of a mutant: script is told the argv
// and which call this is (from 0), and answers the exit code and the output.
type selExecFake struct {
	mu     sync.Mutex
	calls  [][]string
	script func(argv []string, n int) (int, string)
}

func (f *selExecFake) exec(_ context.Context, _ string, _ []string, argv []string, log io.Writer) (int, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, slices.Clone(argv))
	f.mu.Unlock()
	code, out := f.script(argv, n)
	_, err := io.WriteString(log, out)
	return code, err
}

func (f *selExecFake) mutated() (argvs [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if slices.Contains(c, "-overlay") {
			argvs = append(argvs, c)
		}
	}
	return argvs
}

func selHasOverlay(argv []string) bool { return slices.Contains(argv, "-overlay") }

// selFailing is the output of a run that a test of pkg fails.
func selFailing(pkg string) string {
	return "--- FAIL: TestX (0.00s)\nFAIL\nFAIL\t" + pkg + "\t0.1s\n"
}

const selRunSource = "package p\n\nfunc F1() int {\n\tif 1 < 2 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n"

var selRunMutant = selMutant{File: "p/p.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_BOUNDARY"}

// selRunnerFor is a runner over a one-file module, on the index sets given,
// whose `go test` runs are answered by script.
func selRunnerFor(t *testing.T, script func([]string, int) (int, string), sets ...selSet) (*selRunner, *selExecFake) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "p", "p.go"), selRunSource)
	for i := range sets {
		if sets[i].Idx != nil {
			sets[i].Idx.FileHash = map[string]string{"p/p.go": selFileHash(root, "p/p.go")}
		}
	}
	f := &selExecFake{script: script}
	prev := resolveExecFn
	t.Cleanup(func() { resolveExecFn = prev })
	resolveExecFn = f.exec
	r := &selRunner{root: root, cfg: MutantsConfig{}, sets: sets, known: &killChecks{}, log: io.Discard,
		open: func() (string, error) { return root, nil }, env: nil}
	return r, f
}

func selIndexOf(tests map[int][]selTest, doubt map[string]string) *selIndex {
	var all []selTest
	for _, ts := range tests {
		all = append(all, ts...)
	}
	idx := &selIndex{Schema: selSchema, Tests: all, Doubt: doubt, PkgTests: map[string]int{"p": 5, "q": 9},
		Files: map[string][]selBlock{"p/p.go": {{From: 4, FromCol: 1, To: 4, ToCol: 80}, {From: 5, FromCol: 1, To: 5, ToCol: 80}}}}
	for i := range all {
		idx.Files["p/p.go"][0].Tests = append(idx.Files["p/p.go"][0].Tests, i)
	}
	return idx
}

func selTwoSets(doubt map[string]string) (selSet, selSet) {
	unit := selSet{Label: "unit", Idx: selIndexOf(map[int][]selTest{0: {{Pkg: "p", Name: "TestP1"}, {Pkg: "p", Name: "TestP1b"}, {Pkg: "q", Name: "TestQ"}}}, doubt)}
	tagged := selSet{Label: "tags", Tags: []string{"integration"}, Idx: selIndexOf(map[int][]selTest{0: {{Pkg: "p", Name: "TestInteg"}}}, doubt)}
	return unit, tagged
}

func TestSelRunner_RunsTheUnitTestsFirstAndTheTaggedOnesOnlyForASurvivor(t *testing.T) {
	unit, tagged := selTwoSets(nil)
	// the mutant survives the unit tests and is killed by the integration test
	r, f := selRunnerFor(t, func(argv []string, _ int) (int, string) {
		if selHasOverlay(argv) && slices.Contains(argv, "-tags=integration") {
			return 1, selFailing("example.com/m/p")
		}
		return 0, "ok\n"
	}, unit, tagged)
	res, ok := r.judge(context.Background(), selRunMutant, time.Minute, t.TempDir())
	if !ok || res.Status != "caught" || res.Mode != selModeSelected {
		t.Fatalf("result = %+v (ran %v)", res, ok)
	}
	var order []string
	for _, c := range f.mutated() {
		order = append(order, selDescribe(c))
	}
	// unit p, unit q, then the tagged test that kills it, and its one rerun
	want := []string{"-run ^(TestP1|TestP1b)$ ./p", "-run ^(TestQ)$ ./q", "-tags=integration -run ^(TestInteg)$ ./p", "-tags=integration -run ^(TestInteg)$ ./p"}
	if !slices.Equal(order, want) {
		t.Fatalf("mutated runs = %q, want %q", order, want)
	}
	if res.Tests != 4 {
		t.Errorf("tests started = %d, want 4 (TestP1, TestP1b, TestQ, TestInteg)", res.Tests)
	}
}

func TestSelRunner_AMutantTheUnitTestsKillNeverRunsTheTaggedSet(t *testing.T) {
	unit, tagged := selTwoSets(nil)
	r, f := selRunnerFor(t, func(argv []string, _ int) (int, string) {
		if selHasOverlay(argv) {
			return 1, selFailing("example.com/m/p")
		}
		return 0, "ok\n"
	}, unit, tagged)
	res, ok := r.judge(context.Background(), selRunMutant, time.Minute, t.TempDir())
	if !ok || res.Status != "caught" {
		t.Fatalf("result = %+v", res)
	}
	for _, c := range f.calls {
		if slices.Contains(c, "-tags=integration") {
			t.Errorf("the integration set ran for a mutant the unit tests killed: %v", c)
		}
	}
}

func TestSelRunner_ALineNoTestExecutesIsNotCoveredAndNothingIsRun(t *testing.T) {
	unit, tagged := selTwoSets(nil)
	unit.Idx.Files["p/p.go"][0].Tests, tagged.Idx.Files["p/p.go"][0].Tests = nil, nil
	r, f := selRunnerFor(t, func([]string, int) (int, string) { return 0, "" }, unit, tagged)
	res, ok := r.judge(context.Background(), selRunMutant, time.Minute, t.TempDir())
	if !ok || res.Mode != selModeNotCovered || res.Gap.Kind != gapNotCovered || res.Status != "" {
		t.Fatalf("result = %+v (ran %v)", res, ok)
	}
	if len(f.calls) != 0 {
		t.Errorf("a not-covered mutant ran %v", f.calls)
	}
}

func TestSelRunner_EachDoubtHandsTheMutantToTheFullSuite(t *testing.T) {
	for _, why := range []string{selWhyFailed, selWhyList, selWhyBuild, selWhyNoProfile} {
		t.Run(why, func(t *testing.T) {
			unit, tagged := selTwoSets(map[string]string{"p": why})
			r, f := selRunnerFor(t, func([]string, int) (int, string) { return 0, "" }, unit, tagged)
			res, ok := r.judge(context.Background(), selRunMutant, time.Minute, t.TempDir())
			if ok || res.Mode != selModeFull || res.Why != why {
				t.Fatalf("result = %+v (ran %v), want the full suite for %s", res, ok, why)
			}
			if len(f.calls) != 0 {
				t.Errorf("the selection ran %v though the mutant is the full suite's", f.calls)
			}
		})
	}
}

func TestSelRunner_TheFullSuiteRunsTheMutatedPackageWholeUnitThenTagged(t *testing.T) {
	unit, tagged := selTwoSets(map[string]string{"p": selWhyFailed})
	r, f := selRunnerFor(t, func(argv []string, _ int) (int, string) { return 0, "ok\n" }, unit, tagged)
	res := r.settle(context.Background(), selRunMutant, time.Minute, t.TempDir())
	if res.Status != "missed" || res.Mode != selModeFull {
		t.Fatalf("result = %+v", res)
	}
	mutated := f.mutated()
	if len(mutated) != 2 {
		t.Fatalf("%d mutated runs, want the unit package then the tagged one", len(mutated))
	}
	for i, c := range mutated {
		if slices.Contains(c, "-run") {
			t.Errorf("a full run selected tests: %v", c)
		}
		if slices.Contains(c, "-tags=integration") != (i == 1) {
			t.Errorf("run %d = %v, want the tags only on the second", i, c)
		}
		if c[len(c)-1] != "./p" {
			t.Errorf("run %d ends %q, want ./p", i, c[len(c)-1])
		}
	}
}

func TestSelRunner_AKillThatPassesOnTheRerunIsFlakyAndNotACatch(t *testing.T) {
	unit, tagged := selTwoSets(nil)
	mutatedRuns := 0
	var mu sync.Mutex
	r, _ := selRunnerFor(t, func(argv []string, _ int) (int, string) {
		if !selHasOverlay(argv) {
			return 0, "ok\n" // the tests pass without the mutant
		}
		mu.Lock()
		defer mu.Unlock()
		mutatedRuns++
		if mutatedRuns == 1 {
			return 1, selFailing("example.com/m/p") // the first run is the kill
		}
		return 0, "ok\n" // the rerun and everything after passes
	}, unit, tagged)
	res := r.settle(context.Background(), selRunMutant, time.Minute, t.TempDir())
	if res.Gap.Kind != gapFlaky || res.Status == "caught" {
		t.Fatalf("result = %+v, want flaky and not caught", res)
	}
}

func TestSelRunner_AKillThatHoldsOnTheRerunIsCaught(t *testing.T) {
	unit, tagged := selTwoSets(nil)
	r, f := selRunnerFor(t, func(argv []string, _ int) (int, string) {
		if selHasOverlay(argv) {
			return 1, selFailing("example.com/m/p")
		}
		return 0, "ok\n"
	}, unit, tagged)
	res := r.settle(context.Background(), selRunMutant, time.Minute, t.TempDir())
	if res.Status != "caught" || res.Gap.Why != "" {
		t.Fatalf("result = %+v", res)
	}
	if n := len(f.mutated()); n != 2 {
		t.Errorf("%d mutated runs, want the kill and its one rerun", n)
	}
}

func TestSelRunner_TheSummaryCountsSelectedFullAndNotCoveredAndSaysWhatTheCoverageCost(t *testing.T) {
	unit, tagged := selTwoSets(nil)
	r, _ := selRunnerFor(t, func(argv []string, _ int) (int, string) { return 0, "ok\n" }, unit, tagged)
	r.stats.Build, r.stats.Packages, r.stats.Extra = 12*time.Second, 4, 3
	ctx := context.Background()
	r.judge(ctx, selRunMutant, time.Minute, t.TempDir()) // selected, survives
	r.judge(ctx, selRunMutant, time.Minute, t.TempDir())
	r.record(selResult{Mode: selModeSelected, Tests: 6, Gap: commitGap{gapFlaky, "x"}})
	r.record(selResult{Mode: selModeNotCovered})
	r.record(selResult{Mode: selModeFull, Why: selWhyStale})
	r.record(selResult{Mode: selModeFull, Why: selWhyNoEntry})
	r.record(selResult{Mode: selModeFull, Why: selWhyStale})
	// expectation-changed: the summary reads "test(s) per mutant on average", not "tests each", which was wrong for one test
	want := "mutants: selection — 3 ran selected tests (4.7 test(s) per mutant on average), 3 ran the full suite (1 no-entry, 2 stale-shape), 1 not-covered, 1 flaky; " +
		"coverage of 4 package(s), 3 beyond the mutated one, built in 12s"
	if got := r.summary(); got != want {
		t.Errorf("summary =\n%q\nwant\n%q", got, want)
	}
}

// selDescribe is a run's tag, selection and package, the parts a selection
// decides.
func selDescribe(argv []string) string {
	var parts []string
	for i, a := range argv {
		switch {
		case strings.HasPrefix(a, "-tags="):
			parts = append(parts, a)
		case a == "-run":
			parts = append(parts, "-run "+argv[i+1])
		}
	}
	return strings.Join(append(parts, argv[len(argv)-1]), " ")
}

func TestSelRunner_AMutantThatDoesNotCompileIsUnviableAndRunsNothingMore(t *testing.T) {
	unit, tagged := selTwoSets(nil)
	r, f := selRunnerFor(t, func(argv []string, _ int) (int, string) {
		if selHasOverlay(argv) {
			return 1, "FAIL\texample.com/m/p [build failed]\n"
		}
		return 0, "ok\n"
	}, unit, tagged)
	res := r.settle(context.Background(), selRunMutant, time.Minute, t.TempDir())
	if res.Status != "unviable" || res.Gap.Why != "" {
		t.Fatalf("result = %+v", res)
	}
	if n := len(f.mutated()); n != 1 {
		t.Errorf("%d mutated runs, want the one that did not build", n)
	}
}

func TestSettledBySelection_EveryVerdictBecomesTheOutcomeItNames(t *testing.T) {
	m := MutantOutcome{File: "p/p.go", Line: 4, Status: gremlinsNotCovered}
	cases := map[string]struct {
		res     selResult
		status  string
		noteHas string
	}{
		"caught":      {selResult{Status: "caught", Killer: "q: TestQ"}, "caught", "killed by q: TestQ"},
		"unviable":    {selResult{Status: "unviable"}, "unviable", "does not compile"},
		"survived":    {selResult{Status: "missed", Note: "survived the 3 test(s) run for its line in p"}, "missed", "survived the 3 test(s) run for its line in p, every test that executes its line"},
		"flaky":       {selResult{Gap: commitGap{gapFlaky, "failed once"}}, gremlinsNotCovered, "UNRESOLVED: failed once"},
		"budget":      {selResult{Gap: commitGap{gapBudget, "no time"}}, gremlinsNotCovered, "UNRESOLVED: no time"},
		"not covered": {selResult{Gap: commitGap{gapNotCovered, "no test executes it"}}, gremlinsNotCovered, "no test executes it"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := settledBySelection(m, c.res)
			if got.Status != c.status || !strings.Contains(got.Note, c.noteHas) {
				t.Errorf("outcome = %+v, want status %q and a note holding %q", got, c.status, c.noteHas)
			}
		})
	}
}

func TestSelTagSets_TheUnitTestsFirstAndTheRepoTagsOnlyWhenDeclared(t *testing.T) {
	if got := selTagSets(MutantsConfig{}); len(got) != 1 || got[0].Label != "unit" || len(got[0].Tags) != 0 {
		t.Errorf("no tags declared gave %+v, want the unit set alone", got)
	}
	got := selTagSets(MutantsConfig{TestTags: []string{"integration", "pg"}})
	if len(got) != 2 || got[0].Label != "unit" || len(got[0].Tags) != 0 || got[1].Label != "tags" || !slices.Equal(got[1].Tags, []string{"integration", "pg"}) {
		t.Errorf("declared tags gave %+v, want unit then the declared tag set", got)
	}
}
