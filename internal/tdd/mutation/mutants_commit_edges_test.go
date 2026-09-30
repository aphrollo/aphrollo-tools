package mutation

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The edges of the commit-time run: what each new condition does at the limit
// it names and either side of it, and for nothing and for one thing.

func TestCoveredFuncs_ALineNumberTooLargeToParseCoversNothing(t *testing.T) {
	t.Parallel()
	spans := map[string][]funcSpan{"a.go": spansOf("f", 1, 9)}
	profile := "mode: set\nx/a.go:99999999999999999999.1,99999999999999999999.9 1 1\n"
	if got := coveredFuncs(profile, spans); len(got) != 0 {
		t.Errorf("covered = %v, want none", sortedKeys(got))
	}
}

func TestScanTestDecls_SkipsDirectoriesAndUnparseableFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub_test.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "broken_test.go"), "package p\n\nfunc Test_Broken(t *testing.T) {\n")
	mustWrite(t, filepath.Join(dir, "ok_test.go"), "package p\n\nimport \"testing\"\n\nfunc Test_Ok(t *testing.T) {}\n")
	if got, want := testNames(scanTestDecls(dir, "p")), []string{"Test_Ok"}; !slices.Equal(got, want) {
		t.Errorf("testNames = %v, want %v", got, want)
	}
}

func TestTail_KeepsTheLastTwelveLines(t *testing.T) {
	t.Parallel()
	lines := func(n int) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			b.WriteString("line " + strconv.Itoa(i) + "\n")
		}
		return b.String()
	}
	for _, tc := range []struct {
		n         int
		wantFirst string
		wantCount int
	}{
		{0, "", 0},
		{1, "line 1", 1},
		{11, "line 1", 11},
		{12, "line 1", 12},
		{13, "line 2", 12},
		{20, "line 9", 12},
	} {
		got := tail(lines(tc.n))
		first, _, _ := strings.Cut(got, "\n")
		count := 0
		if got != "" {
			count = strings.Count(got, "\n") + 1
		}
		if first != tc.wantFirst || count != tc.wantCount {
			t.Errorf("%d lines: tail starts %q with %d lines, want %q with %d", tc.n, first, count, tc.wantFirst, tc.wantCount)
		}
	}
}

func TestBuildTestMap_AListFailureIsAnError(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", listCode: 3}
	root := buildFixture(t, tc)
	_, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)
	if err == nil || built || !strings.Contains(err.Error(), "listing the tests") {
		t.Errorf("buildTestMap = built %v, err %v, want the listing failure named", built, err)
	}
}

func TestBuildTestMap_ACompileThatCannotRunIsAnError(t *testing.T) {
	tc := &fakeToolchain{compile: func([]string) (int, error) { return 0, errors.New("no go") }}
	root := buildFixture(t, tc)
	if _, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard); err == nil || built {
		t.Errorf("buildTestMap = built %v, err %v, want an error", built, err)
	}
}

func TestBuildTestMap_AListingFailureIsAnError(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n"}
	root := buildFixture(t, tc)
	t.Cleanup(setGoListForTest(func(context.Context, string, string) (string, error) { return "", errors.New("go list broke") }))
	_, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)
	if err == nil || built || len(tc.calls) != 0 {
		t.Errorf("buildTestMap = built %v, err %v after %d commands, want an error before any command", built, err, len(tc.calls))
	}
}

func TestRefreshTestMaps_APackageWithNoTestsIsNeitherBuiltNorCurrent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{compile: func([]string) (int, error) { return 0, nil }}
	root := buildFixture(t, tc)
	built, fresh, err := refreshTestMaps(context.Background(), root, MutantsConfig{}, []string{"internal/p"}, 1, io.Discard)
	if err != nil || built != 0 || fresh != 0 {
		t.Errorf("refresh = built %d fresh %d err %v, want zeros", built, fresh, err)
	}
	if _, ok := loadTestMap(root, "internal/p"); ok {
		t.Error("a map was kept for a package with no tests")
	}
}

func TestRefreshTestMaps_AFailureNamesItAndTheOthersAreStillDone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	realList := goListFn
	t.Cleanup(setGoListForTest(func(ctx context.Context, r, dir string) (string, error) {
		if dir == "internal/broken" {
			return "", errors.New("go list broke")
		}
		return realList(ctx, r, dir)
	}))
	built, fresh, err := refreshTestMaps(context.Background(), root, MutantsConfig{}, []string{"internal/broken", "internal/p"}, 1, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "internal/broken") || built != 1 || fresh != 0 {
		t.Errorf("refresh = built %d fresh %d err %v, want the broken package named and the other built", built, fresh, err)
	}
}

// At most the worker count run at once, and a count below one is one.
func TestRunCommitMutants_WorkerCountsBelowOneRunOneWorker(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	for _, workers := range []int{0, -3} {
		runs := runCommitMutants(context.Background(), root, MutantsConfig{}, planFor(nil), []commitMutant{kindMutant, kindMutant}, workers, time.Minute, io.Discard)
		if len(runs) != 2 || runs[0].Outcome.Status != "missed" || runs[1].Outcome.Status != "missed" {
			t.Errorf("workers %d: runs = %+v, want both mutants judged", workers, runs)
		}
	}
}

func TestRunCommitMutants_APackageWithNoPlanRunsTheWholePackage(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	runs := runCommitMutants(context.Background(), root, MutantsConfig{}, map[string]*commitPlan{}, []commitMutant{kindMutant}, 1, time.Minute, io.Discard)
	if !runs[0].WholePackage || s.calls[0].Run != "" {
		t.Errorf("whole %v with -run %q, want the whole package without a plan", runs[0].WholePackage, s.calls[0].Run)
	}
}

func TestRunCommitMutants_WhatCannotBeSetUpIsARunnerGapNotASurvivor(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	missingFile := commitMutant{File: "gate/absent.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_BOUNDARY", Func: "Kind"}
	noToken := commitMutant{File: "gate/gate.go", Line: 4, Col: 1, Mutation: "CONDITIONALS_BOUNDARY", Func: "Kind"}
	for name, m := range map[string]commitMutant{"a source the copy lacks": missingFile, "a position holding no token": noToken} {
		got := runCommitOnce(t, root, planFor(nil), m, time.Minute)
		if got.NotMeasured == "" || got.GapKind != gapRunner || got.Outcome.Status != "" {
			t.Errorf("%s: outcome %q gap %q (%s), want a runner gap", name, got.Outcome.Status, got.GapKind, got.NotMeasured)
		}
	}
	if s.count() != 0 {
		t.Errorf("go test ran %d times for mutants that could not be set up", s.count())
	}
}

func TestRunCommitMutants_ARootThatIsNoRepositoryIsARunnerGap(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\n" })
	runs := runCommitMutants(context.Background(), t.TempDir(), MutantsConfig{}, planFor(nil), []commitMutant{kindMutant}, 1, time.Minute, io.Discard)
	if runs[0].GapKind != gapRunner || !strings.Contains(runs[0].NotMeasured, "git repository") {
		t.Errorf("run = %+v, want a runner gap naming the missing repository", runs[0])
	}
}

func TestRunCommitMutants_AGoTestThatCannotStartIsARunnerGap(t *testing.T) {
	root := commitRoot(t)
	prev := resolveExecFn
	resolveExecFn = func(context.Context, string, []string, []string, io.Writer) (int, error) {
		return 0, errors.New("exec: go not found")
	}
	t.Cleanup(func() { resolveExecFn = prev })
	got := runCommitOnce(t, root, planFor(nil), kindMutant, time.Minute)
	if got.GapKind != gapRunner || !strings.Contains(got.NotMeasured, "could not start") {
		t.Errorf("run = kind %q (%s), want a runner gap saying it could not start", got.GapKind, got.NotMeasured)
	}
}

func TestMutantsAtCommitStage_ABrokenConfigIsInert(t *testing.T) {
	_, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\nmutation-receipt = true\n")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
	if res := mutantsAtCommitStage("precommit", root); res.Blocked || res.Message != "" || s.count() != 0 {
		t.Errorf("stage = %+v after %d runs, want nothing for a config the merge gate refuses", res, s.count())
	}
}

// Git that cannot say what the commit adds is a box that cannot measure it.
func TestMutantsAtCommitStage_AGitFailureIsNotMeasured(t *testing.T) {
	for name, failing := range map[string]string{"the staged diff": "--cached", "the unstaged listing": "--name-only"} {
		cfgDir, root := commitStage(t, "")
		s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
		real := gitDiffOutFn
		t.Cleanup(setGitDiffOutForTest(func(dir string, args ...string) (string, string, error) {
			if slices.Contains(args, failing) {
				return "", "fatal: broken", errors.New("exit status 128")
			}
			return real(dir, args...)
		}))
		var res GateResult
		stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })
		if res.Blocked || s.count() != 0 || !strings.Contains(stderr, "NOT MEASURED") {
			t.Errorf("%s: stage %+v after %d runs, stderr %q, want NOT MEASURED and nothing run", name, res, s.count(), stderr)
		}
		if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-unmeasured:commit-diff") {
			t.Errorf("%s: gate.log = %q, want the gap counted", name, log)
		}
	}
}

func TestCommitVerdict_ReportsWhatWasNotMeasuredBesideWhatWas(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	root := t.TempDir()
	caught := commitRun{Mutant: kindMutant, Outcome: MutantOutcome{File: "gate/gate.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_BOUNDARY", Status: "caught"}}
	cutA := commitRun{Mutant: kindMutant, NotMeasured: "cut", GapKind: gapBudget}
	cutB := commitRun{Mutant: labelMutant, NotMeasured: "cut", GapKind: gapBudget}
	unconfirmed := commitRun{Mutant: labelMutant, NotMeasured: "flaky", GapKind: gapUnconfirmed}

	var res GateResult
	stderr := captureStderr(t, func() {
		res = commitVerdict("precommit", root, MutantsConfig{}, []commitRun{caught, cutA, cutB, unconfirmed}, 2*time.Second)
	})

	if res.Blocked {
		t.Errorf("a run with one caught mutant and three unmeasured was refused: %s", res.Message)
	}
	if !strings.Contains(stderr, "1 tested, 1 caught") || !strings.Contains(stderr, "NOT MEASURED (2 budget, 1 unconfirmed)") {
		t.Errorf("stderr = %q, want the measured counts and the unmeasured summary", stderr)
	}
	log := gateLogText(t, cfgDir)
	if strings.Count(log, "mutants-unmeasured:commit-budget") != 1 || strings.Count(log, "mutants-unmeasured:commit-unconfirmed") != 1 {
		t.Errorf("gate.log = %q, want each kind counted once however many mutants it left", log)
	}
}

func TestCommitVerdict_NothingMeasuredIsNotRefusedAndSaysSo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var res GateResult
	stderr := captureStderr(t, func() {
		res = commitVerdict("precommit", t.TempDir(), MutantsConfig{}, []commitRun{{NotMeasured: "cut", GapKind: gapBudget}}, time.Second)
	})
	if res.Blocked || !strings.Contains(stderr, "NOT MEASURED") {
		t.Errorf("result %+v stderr %q, want an unrefused NOT MEASURED", res, stderr)
	}
}

func TestRunMutantsTestMap_ANonGoRepoIsInert(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepoForCommit(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n")
	var out, errOut strings.Builder
	if code := RunMutantsTestMap(root, []string{"x"}, &out, &errOut); code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("exit %d stdout %q stderr %q, want a silent 0", code, out.String(), errOut.String())
	}
}

func TestRunMutantsTestMap_APackageListFailureIsAnError(t *testing.T) {
	tc := &fakeToolchain{}
	root := testMapVerbFixture(t, true, tc)
	prev := goTestedPackagesFn
	goTestedPackagesFn = func(context.Context, string) (string, error) { return "", errors.New("go list broke") }
	t.Cleanup(func() { goTestedPackagesFn = prev })
	var out, errOut strings.Builder
	if code := RunMutantsTestMap(root, nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "go list broke") {
		t.Errorf("exit %d stderr %q, want 1 and the failure", code, errOut.String())
	}
}

func TestRunMutantsCommit_ABrokenConfigIsExitOne(t *testing.T) {
	_, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-receipt = true\n")
	var out, errOut strings.Builder
	if code := RunMutantsCommit(root, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "retired") {
		t.Errorf("exit %d stderr %q, want 1 and the refusal", code, errOut.String())
	}
}

func TestEditStage_ANonGoRepoIsInert(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepoForCommit(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })
	if res := editStage(root, "src/lib.go"); res.Blocked || s.count() != 0 {
		t.Errorf("stage = %+v after %d runs, want inert", res, s.count())
	}
}

func TestEditAddedLines_AGitFailureIsAnError(t *testing.T) {
	root := editRepo(t)
	for name, failing := range map[string]string{"the diff": "diff", "the untracked listing": "ls-files"} {
		real := gitDiffOutFn
		restore := setGitDiffOutForTest(func(dir string, args ...string) (string, string, error) {
			if slices.Contains(args, failing) {
				return "", "fatal: broken", errors.New("exit status 128")
			}
			return real(dir, args...)
		})
		_, err := editAddedLines(root, "gate/never-tracked.go")
		restore()
		if err == nil {
			t.Errorf("%s failed and editAddedLines said nothing", name)
		}
	}
}

func TestEditAddedLines_AnUnreadableNewFileIsAnError(t *testing.T) {
	root := editRepo(t)
	if err := os.Symlink(filepath.Join(root, "nowhere.go"), filepath.Join(root, "gate", "dangling.go")); err != nil {
		// skip-ok: a box that cannot create symlinks cannot make the unreadable file this test needs
		t.Skip("symlinks unavailable: " + err.Error())
	}
	if _, err := editAddedLines(root, "gate/dangling.go"); err == nil {
		t.Error("an untracked file that cannot be read was measured as if it were empty")
	}
}

func TestRunMutantsEdit_ACannotRecordFailsLoudly(t *testing.T) {
	root := editRepo(t)
	scriptGo(t, killsUnderTheMutant)
	var errOut strings.Builder
	done := filepath.Join(t.TempDir(), "missing-dir", "done")
	if code := RunMutantsEdit(root, "gate/gate.go", done, &errOut); code != 1 || !strings.Contains(errOut.String(), "recording the result") {
		t.Errorf("exit %d stderr %q, want 1 and the failure named", code, errOut.String())
	}
}
