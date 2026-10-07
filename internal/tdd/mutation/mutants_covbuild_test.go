package mutation

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	covbuildSource = "package p\n\nfunc f() int {\n\treturn 1\n}\n\nfunc g() int {\n\treturn 2\n}\n\nfunc h() int {\n\treturn 3\n}\n"
	covbuildTests  = "package p\n\nimport \"testing\"\n\nfunc Test_A(t *testing.T) { _ = f() }\nfunc Test_B(t *testing.T) { _ = g() }\nfunc Test_C(t *testing.T) { _ = h() }\n"
)

// covbuildProfile is a cover profile of the fixture's three functions, one
// block each at lines 4, 8 and 12, the named ones executed.
func covbuildProfile(hits ...bool) string {
	var b strings.Builder
	b.WriteString("mode: set\n")
	for i, line := range []string{"4", "8", "12"} {
		count := "0"
		if hits[i] {
			count = "1"
		}
		b.WriteString("x/internal/p/p.go:" + line + ".9," + line + ".10 1 " + count + "\n")
	}
	return b.String()
}

// covbuildRepo is a repository holding the fixture package on a fake
// toolchain in which each Test_X runs the function of its letter.
func covbuildRepo(t *testing.T, tc *fakeToolchain) string {
	t.Helper()
	root := makeGoRepo(t)
	mustWrite(t, filepath.Join(root, "internal", "p", "p.go"), covbuildSource)
	mustWrite(t, filepath.Join(root, "internal", "p", "p_test.go"), covbuildTests)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "the package under test")
	if tc.list == "" {
		tc.list = "Test_A\nTest_B\nTest_C\n"
	}
	if tc.profiles == nil {
		tc.profiles = map[string]string{
			"Test_A": covbuildProfile(true, false, false),
			"Test_B": covbuildProfile(false, true, false),
			"Test_C": covbuildProfile(false, false, true),
		}
	}
	prev := testMapExecFn
	t.Cleanup(func() { testMapExecFn = prev })
	testMapExecFn = tc.exec
	return root
}

func covbuildMutant(line int) commitMutant {
	return commitMutant{File: "internal/p/p.go", Line: line, Col: 9, Mutation: "ARITHMETIC_BASE"}
}

func covbuildAsk(t *testing.T, root string, workers int, lines ...int) covResult {
	t.Helper()
	return covbuildAskCtx(t, context.Background(), root, MutantsConfig{}, workers, nil, lines...)
}

func covbuildAskCtx(t *testing.T, ctx context.Context, root string, cfg MutantsConfig, workers int, box *commitBox, lines ...int) covResult {
	t.Helper()
	var mutants []commitMutant
	for _, line := range lines {
		mutants = append(mutants, covbuildMutant(line))
	}
	res, err := ensureCoverage(ctx, root, cfg, covRequest{Dir: "internal/p", Mutants: mutants, Workers: workers, Box: box}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestEnsureCoverage_ColdMeasuresOnlyTheTestsThatCanReachTheChangedFunction(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	res := covbuildAsk(t, root, 2, 4)
	if compiles, solo := testRuns(tc); compiles != 1 || solo != 1 {
		t.Fatalf("%d compiles and %d solo runs, want 1 and 1 (Test_A alone reaches f)", compiles, solo)
	}
	if !res.Map.Partial || res.Unmeasured != 2 || res.Measured != 1 {
		t.Fatalf("result = %+v", res)
	}
	if got, listed := res.Map.testsAt("p.go", 4); !listed || !slices.Equal(got, []string{"Test_A"}) {
		t.Fatalf("tests at f = %v (listed %v)", got, listed)
	}
	if got, listed := res.Map.testsAt("p.go", 8); !listed || len(got) != 0 {
		t.Fatalf("g is a block no measured test ran: %v (listed %v)", got, listed)
	}
}

func TestEnsureCoverage_ASecondCommitOfTheSameTreeRunsNothing(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	covbuildAsk(t, root, 2, 4)
	before := len(tc.calls)
	res := covbuildAsk(t, root, 2, 4)
	if len(tc.calls) != before {
		t.Fatalf("the warm call ran %d commands, want none", len(tc.calls)-before)
	}
	if res.Kept != 1 || res.Measured != 0 {
		t.Fatalf("result = %+v", res)
	}
	if got, _ := res.Map.testsAt("p.go", 4); !slices.Equal(got, []string{"Test_A"}) {
		t.Fatalf("tests at f = %v", got)
	}
}

func TestEnsureCoverage_AnEditedFunctionRemeasuresOnlyItsTests(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	covbuildAsk(t, root, 2, 4, 8)
	if _, solo := testRuns(tc); solo != 2 {
		t.Fatalf("setup: %d solo runs, want 2", solo)
	}
	// f grows a line, so g and h move down by one; only f changed.
	mustWrite(t, filepath.Join(root, "internal", "p", "p.go"),
		strings.Replace(covbuildSource, "return 1", "x := 1\n\treturn x", 1))
	tc.profiles["Test_A"] = "mode: set\nx/internal/p/p.go:4.9,5.10 2 1\nx/internal/p/p.go:9.9,9.10 1 0\nx/internal/p/p.go:13.9,13.10 1 0\n"
	_, soloBefore := testRuns(tc)
	res := covbuildAsk(t, root, 2, 5)
	if _, solo := testRuns(tc); solo-soloBefore != 1 {
		t.Fatalf("%d solo runs after the edit, want 1 (Test_A, the test that ran f)", solo-soloBefore)
	}
	if got, listed := res.Map.testsAt("p.go", 9); !listed || !slices.Equal(got, []string{"Test_B"}) {
		t.Fatalf("g moved to line 9 and kept Test_B: %v (listed %v)", got, listed)
	}
	if got, _ := res.Map.testsAt("p.go", 5); !slices.Equal(got, []string{"Test_A"}) {
		t.Fatalf("tests at the edited f = %v", got)
	}
}

func TestEnsureCoverage_AnotherFunctionIsMeasuredOnTopOfWhatIsKept(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	covbuildAsk(t, root, 1, 4)
	res := covbuildAsk(t, root, 1, 8)
	if res.Kept != 1 || res.Measured != 1 || res.Unmeasured != 1 {
		t.Fatalf("result = %+v, want Test_A kept, Test_B measured, Test_C not", res)
	}
}

// A deadline ends the run, never the work already done: every test that
// finished is in the store, and the next commit measures only the rest.
func TestEnsureCoverage_ACancelledRunKeepsTheTestsThatFinished(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var soloDone atomic.Int32
	testMapExecFn = func(c context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		code, err := tc.exec(c, dir, env, argv, log)
		if prefixValue(argv, "-test.run=") != "" && soloDone.Add(1) == 1 {
			cancel()
		}
		return code, err
	}
	first := covbuildAskCtx(t, ctx, root, MutantsConfig{}, 1, nil, 4, 8, 12)
	if !first.Cut || first.Measured != 1 {
		t.Fatalf("first = %+v, want cut off after one finished test", first)
	}
	testMapExecFn = tc.exec
	second := covbuildAsk(t, root, 1, 4, 8, 12)
	if second.Kept != 1 || second.Measured != 2 || second.Unmeasured != 0 || second.Map.Partial {
		t.Fatalf("second = %+v, want the finished test kept and the other two measured", second)
	}
}

func TestEnsureCoverage_ATestThatWritesNoProfileIsUnknownAndNotKept(t *testing.T) {
	tc := &fakeToolchain{profiles: map[string]string{"Test_B": covbuildProfile(false, true, false), "Test_C": covbuildProfile(false, false, true)}}
	root := covbuildRepo(t, tc)
	res := covbuildAsk(t, root, 1, 4)
	if !slices.Equal(res.Map.Unknown, []string{"Test_A"}) {
		t.Fatalf("unknown = %v", res.Map.Unknown)
	}
	_, soloBefore := testRuns(tc)
	covbuildAsk(t, root, 1, 4)
	if _, solo := testRuns(tc); solo-soloBefore != 1 {
		t.Fatalf("a test with no profile is asked again, %d solo runs", solo-soloBefore)
	}
}

func TestEnsureCoverage_ATestTheBinaryDoesNotListIsNotCountedUnmeasured(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\nTest_B\n"}
	root := covbuildRepo(t, tc)
	res := covbuildAsk(t, root, 1, 4, 12)
	if res.Unmeasured != 1 {
		t.Fatalf("result = %+v: Test_C is not in the binary (a build tag leaves it out) and Test_B is not measured", res)
	}
}

func TestEnsureCoverage_APackageWithNoTestFunctionsRunsNothing(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	mustWrite(t, filepath.Join(root, "internal", "p", "p_test.go"), "package p\n")
	res := covbuildAsk(t, root, 1, 4)
	if res.Built || len(tc.calls) != 0 {
		t.Fatalf("built %v after %d commands", res.Built, len(tc.calls))
	}
}

func TestEnsureCoverage_ACompileFailureIsAnErrorAndKeepsNothing(t *testing.T) {
	tc := &fakeToolchain{compile: func([]string) (int, error) { return 2, nil }}
	root := covbuildRepo(t, tc)
	if _, err := ensureCoverage(context.Background(), root, MutantsConfig{}, covRequest{Dir: "internal/p", Mutants: []commitMutant{covbuildMutant(4)}, Workers: 1}, io.Discard); err == nil {
		t.Fatal("want an error")
	}
	if entries, _ := filepath.Glob(filepath.Join(CoverCacheDir(root), "*.json")); len(entries) != 0 {
		t.Fatalf("a failed build kept %v", entries)
	}
}

// The build runs in the box the mutant runs share, so the lane is copied once.
func TestEnsureCoverage_TheBuildRunsInTheSharedBox(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	box := &commitBox{root: root, worker: 0}
	defer box.close()
	covbuildAskCtx(t, context.Background(), root, MutantsConfig{}, 1, box, 4)
	boxRoot, err := box.open()
	if err != nil {
		t.Fatal(err)
	}
	if !tc.dirs[boxRoot] {
		t.Fatalf("no command ran in the shared box %s; ran in %v", boxRoot, tc.dirs)
	}
	if tc.dirs[root] {
		t.Fatalf("a command ran in the checkout %s itself", root)
	}
}

func TestEnsureCoverage_DeclaredTagsReachTheCompileAndKeyTheStore(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	cfg := MutantsConfig{TestTags: []string{"integration"}}
	covbuildAskCtx(t, context.Background(), root, cfg, 1, nil, 4)
	if !slices.Contains(tc.calls[0], "-tags=integration") {
		t.Fatalf("compile argv = %v", tc.calls[0])
	}
	before := len(tc.calls)
	covbuildAsk(t, root, 1, 4)
	if len(tc.calls) == before {
		t.Fatal("a store measured under other tags was used")
	}
}
