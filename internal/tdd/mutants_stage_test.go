package tdd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pre-merge stage is where a mutation measurement finally judges the tree
// that is about to land. What it must get right is not the measurement — the
// library owns that — but WHICH merges it judges and WHAT it hands back: a
// repo that declared nothing is never measured, a repo that declared a retired
// key is refused before anything expensive starts, a conclusion that is not a
// merge into trunk is stood down rather than refused, and a measurement runs
// only after the suites have already proved the merged tree green.

// makeMergeInProgressRepo builds the exact state pre-merge-commit fires in: a
// trunk that has moved on since the lane forked, the lane merged into it with
// --no-commit, so HEAD is still trunk and the merge result exists only in the
// index and the working tree. The lane changes one crate source and trunk
// changes another, so a base taken at HEAD and a base taken at the merge base
// select different files.
func makeMergeInProgressRepo(t *testing.T) string {
	t.Helper()
	root, _ := makeForkedRepo(t)
	write(t, root, "crates/a/src/other.rs", "pub fn other() -> i32 { 7 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk moves on")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "-q", "lane")
	requireMergeState(t, root, "MERGE_HEAD")
	return root
}

// makeForkedRepo commits a base, puts one crate-source change on a `lane`
// branch and returns to trunk. Every fixture below starts here and differs
// only in what it does with the lane afterwards.
func makeForkedRepo(t *testing.T) (root, trunk string) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	writeMeasureBase(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	trunk = currentBranch(t, root)
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a - b }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	gitDo(t, root, "checkout", "-q", trunk)
	return root, trunk
}

// requireMergeState pins what a fixture claims to have set up. Every stand-down
// below is keyed on which of git's in-progress refs exists, so a fixture that
// quietly failed to create one would make its test pass for the wrong reason.
func requireMergeState(t *testing.T, root, want string) {
	t.Helper()
	if got := mergeInProgressRef(root); got != want {
		t.Fatalf("fixture invariant broken: in-progress ref is %q, want %q", got, want)
	}
}

// mergeStageFixture is a merge in progress, a state dir of its own and a drive
// with room, so a test under it is about the step it names.
func mergeStageFixture(t *testing.T) (cfgDir, root string) {
	t.Helper()
	cfgDir = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	return cfgDir, makeMergeInProgressRepo(t)
}

// declareMutantsAtMerge is the one key that turns the stage on.
func declareMutantsAtMerge(t *testing.T, root string) {
	t.Helper()
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-merge = true\n")
}

// requireMutantsLogLine fails unless gate.log carries a PARSEABLE line with
// this verdict, under this stage and naming the mutation stage as its command.
// A verdict nothing can parse is a verdict `gate stats` never counts, which is
// the failure the stage this replaces made 141 times.
func requireMutantsLogLine(t *testing.T, cfgDir, stage, verdict string) {
	t.Helper()
	text := gateLogText(t, cfgDir)
	for line := range strings.SplitSeq(text, "\n") {
		e, ok := parseGateLine(line)
		if !ok || e.verdict != verdict {
			continue
		}
		if e.stage != stage {
			t.Errorf("verdict %q logged under stage %q, want %q: %s", verdict, e.stage, stage, line)
		}
		if !strings.Contains(line, " mutants ") {
			t.Errorf("verdict %q names no command: %s", verdict, line)
		}
		return
	}
	t.Fatalf("no parseable gate.log line with verdict %q, got:\n%s", verdict, text)
}

// requireNoMutantsMeasurement fails when gate.log carries any verdict from a
// run that reached the tool: "it was never measured" and "it measured clean"
// are different claims.
func requireNoMutantsMeasurement(t *testing.T, cfgDir string) {
	t.Helper()
	for line := range strings.SplitSeq(gateLogText(t, cfgDir), "\n") {
		if e, ok := parseGateLine(line); ok && strings.HasPrefix(e.verdict, "mutants-passed:") {
			t.Errorf("a measurement was recorded where none should have run: %s", line)
		}
	}
}

// A repo that declared nothing is not measured, and says so durably: "the
// stage did not run" and "the stage ran and found nothing" are different
// facts, and `gate stats` can only tell them apart if the skip is logged.
func TestMutantsStage_NotDeclaredLogsSkipAndPasses(t *testing.T) {
	cfgDir, root := mergeStageFixture(t)
	calls := stubMutantsExec(t, nil)

	res := mutantsStage(premergeDisplayName, root)

	if res.Blocked {
		t.Fatalf("a repo that declared nothing must not be refused: %s", res.Message)
	}
	if len(*calls) != 0 {
		t.Fatalf("measured a repo that declared nothing: %+v", *calls)
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-skipped:not-declared")
}

// Criterion 4: at pre-merge-commit HEAD is still trunk, so the base is the
// merge base with the INCOMING tip and the whole merged tree is what gets
// measured. A base taken at HEAD would hand the runner a diff missing every
// change trunk itself made since the lane forked.
func TestMutantsStage_MeasuresTheMergedTreeAgainstTheMergeBase(t *testing.T) {
	_, root := mergeStageFixture(t)
	declareMutantsAtMerge(t, root)
	calls := stubMutantsExec(t, func(int, measuredCall) (int, error) {
		writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace - with +", Package: "a", Status: "caught"})
		return 0, nil
	})

	mutantsStage(premergeDisplayName, root)

	if len(*calls) != 1 {
		t.Fatalf("ran the tool %d time(s), want exactly one measurement", len(*calls))
	}
	diff := readFileString(t, argvValueOf(t, (*calls)[0].Argv, "--in-diff"))
	if !strings.Contains(diff, "a - b") {
		t.Errorf("measured diff carries no hunk for the lane's own change:\n%s", diff)
	}
	if !strings.Contains(diff, "pub fn other") {
		t.Errorf("measured diff stops at HEAD instead of the merge base — trunk's own change since the fork is missing:\n%s", diff)
	}
}

// argvValueOf is the value of one flag in a rendered command line.
func argvValueOf(t *testing.T, argv []string, flag string) string {
	t.Helper()
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("argv carries no %s: %v", flag, argv)
	return ""
}

// A conflicted cherry-pick or revert is concluded with `git commit`, which
// routes through this same routine — and neither writes MERGE_HEAD nor a
// `merge <ref>` reflog action. Refusing them for "no lane tip" would make
// every conflicted cherry-pick in a repo that declares the key uncommittable,
// which is the gate breaking work it has no business judging: nothing is being
// merged, so there is nothing to measure.
func TestMutantsStage_CherryPickInProgressIsNotMeasured(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root := makeCherryPickInProgressRepo(t)
	declareMutantsAtMerge(t, root)
	calls := stubMutantsExec(t, nil)

	res := mutantsStage(premergeDisplayName, root)

	if res.Blocked {
		t.Fatalf("a conflicted cherry-pick must not be refused by the mutation stage: %s", res.Message)
	}
	if len(*calls) != 0 {
		t.Errorf("measured a cherry-pick: %+v", *calls)
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-skipped:not-a-merge")
}

// makeCherryPickInProgressRepo leaves a CONFLICTED cherry-pick in the tree: a
// clean one commits itself and leaves no in-progress ref to judge.
func makeCherryPickInProgressRepo(t *testing.T) string {
	t.Helper()
	root, trunk := makeForkedRepo(t)
	pick := strings.TrimSpace(gitOutT(t, root, "rev-parse", "lane"))
	write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a * b }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk edits the same line")
	if _, err := git(root, "cherry-pick", pick); err == nil {
		t.Fatalf("fixture invariant broken: the cherry-pick onto %s was expected to conflict", trunk)
	}
	requireMergeState(t, root, "CHERRY_PICK_HEAD")
	return root
}

// A catch-up merge of trunk INTO a lane is the push guard's own remedy, and it
// lands nothing: the lane is not being merged anywhere. Measuring it charges
// the lane for every change trunk made since the fork — and a survivor trunk
// already accepted would refuse the lane's catch-up.
func TestMutantsStage_CatchUpMergeInsideALaneIsNotMeasured(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root := makeCatchUpMergeRepo(t)
	declareMutantsAtMerge(t, root)
	calls := stubMutantsExec(t, nil)

	res := mutantsStage(premergeDisplayName, root)

	if res.Blocked {
		t.Fatalf("a catch-up merge must not be refused: %s", res.Message)
	}
	if len(*calls) != 0 {
		t.Errorf("measured a merge of trunk into a lane: %+v", *calls)
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-skipped:catch-up")
}

// makeCatchUpMergeRepo has HEAD on a lane branch with trunk merged INTO it,
// the mirror image of makeMergeInProgressRepo.
func makeCatchUpMergeRepo(t *testing.T) string {
	t.Helper()
	root, trunk := makeForkedRepo(t)
	write(t, root, "crates/a/src/other.rs", "pub fn other() -> i32 { 7 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk moves on")
	gitDo(t, root, "checkout", "-q", "lane")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "-q", trunk)
	requireMergeState(t, root, "MERGE_HEAD")
	if branch := currentBranch(t, root); branchIsTrunk(branch, trunkBranch(root)) {
		t.Fatalf("fixture invariant broken: HEAD is %q, which the gate reads as trunk", branch)
	}
	return root
}

// The gate failing on its own inputs says which input. A clean automerge has
// no MERGE_HEAD yet and only GIT_REFLOG_ACTION names the branch coming in, so
// a stage that can resolve neither cannot know what it would be measuring.
func TestMutantsStage_NoLaneTipRefusesNamingBothSignals(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, _ := makeMeasureRepo(t, laneSource)
	t.Setenv(reflogActionEnv, "")
	declareMutantsAtMerge(t, root)
	if RepoRoot(root) == "" {
		t.Fatal("fixture invariant broken: the refusal has to come from a real repo with no merge in progress")
	}
	calls := stubMutantsExec(t, nil)

	res := mutantsStage(premergeDisplayName, root)

	if !res.Blocked {
		t.Fatalf("a merge the gate cannot identify must be refused, got %+v", res)
	}
	for _, want := range []string{"MERGE_HEAD", reflogActionEnv} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message = %q, want it to name %s", res.Message, want)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("measured something anyway: %+v", *calls)
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-refused:no-lane-tip")
}

// The refusal a merge reads is the verdict itself, verbatim: the surviving
// mutant first, then the counts, then the remedy. A stage that summarised it
// into a line of its own would bury the finding the whole run exists to
// produce (criterion 12).
func TestMutantsStage_RefusesWithVerdictMessageVerbatim(t *testing.T) {
	_, root := mergeStageFixture(t)
	declareMutantsAtMerge(t, root)
	survivor := MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
		Mutation: "replace - with +", Package: "a", Status: "missed"}
	stubMutantsExec(t, func(int, measuredCall) (int, error) {
		writeOutcomes(t, root, survivor)
		return 2, nil
	})

	res := mutantsStage(premergeDisplayName, root)

	if !res.Blocked {
		t.Fatalf("an unaccepted survivor must refuse the merge, got %+v", res)
	}
	lines := strings.Split(strings.TrimSpace(res.Message), "\n")
	want := mutantLineOf(survivor.File, survivor.Line, survivor.Col, survivor.Mutation)
	if lines[0] != want {
		t.Errorf("first line = %q, want the surviving mutant %q", lines[0], want)
	}
	if lines[1] != "mutants: 1 tested, 0 caught, 0 unviable, 1 missed (0 accepted), 0 unmeasured" {
		t.Errorf("second line = %q, want the counts", lines[1])
	}
}

// A runner that never started measured nothing, which is not "nothing
// survived". The refusal carries what went wrong and is counted under its own
// reason, so a box missing the tool is not filed as a lane with a survivor.
func TestMutantsStage_RunnerThatNeverStartedIsARefusal(t *testing.T) {
	cfgDir, root := mergeStageFixture(t)
	declareMutantsAtMerge(t, root)
	stubMutantsExec(t, func(int, measuredCall) (int, error) {
		return 0, errors.New("exec: \"cargo\": executable file not found in %PATH%")
	})

	res := mutantsStage(premergeDisplayName, root)

	if !res.Blocked {
		t.Fatalf("a runner that never started must refuse the merge, got %+v", res)
	}
	if !strings.Contains(res.Message, "could not start") {
		t.Errorf("message = %q, want it to say the run never started", res.Message)
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-refused:runner-failed")
	requireNoMutantsMeasurement(t, cfgDir)
}

// A merge that measured clean carries the counts into the gate's own notes:
// a stage that passed silently is one nobody can tell from a stage that never
// ran.
func TestMechanical_PassingMeasurementAppendsItsCountsToTheNotes(t *testing.T) {
	_, root := mergeStageFixture(t)
	declareMutantsAtMerge(t, root)
	stubMutantsExec(t, func(int, measuredCall) (int, error) {
		writeOutcomes(t, root,
			MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36, Mutation: "replace - with +", Package: "a", Status: "caught"},
			MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 40, Mutation: "replace add -> i32 with 0", Package: "a", Status: "caught"})
		return 0, nil
	})

	res := Mechanical(root, passingSuiteRunner())

	if res.Blocked {
		t.Fatalf("a merge whose mutants were all caught must not be refused: %s", res.Message)
	}
	want := "mutants: 2 tested, 2 caught, 0 unviable, 0 missed (0 accepted), 0 unmeasured"
	if !strings.Contains(res.Message, want) {
		t.Errorf("notes =\n%s\nwant the counts %q", res.Message, want)
	}
}

// passingSuiteRunner answers every stage green without running anything, so a
// test about the mutation stage is not about whether cargo is installed.
func passingSuiteRunner() SuiteRunner {
	return func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }
}

// Criterion 2: a repo still declaring `mutation-receipt = true` believes it is
// gated and is not. It is told so before the merge spends a single suite on it,
// which is the difference between a one-line correction and a twenty-minute one.
func TestMechanical_RetiredKeyRefusesBeforeTheSuites(t *testing.T) {
	cfgDir, root := mergeStageFixture(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-receipt = true\n")
	var seen []Runner
	calls := stubMutantsExec(t, nil)

	res := Mechanical(root, recordRunner(&seen, root))

	if !res.Blocked {
		t.Fatalf("a retired key must refuse the merge, got %+v", res)
	}
	for _, want := range []string{"mutation-receipt", "mutants-at-merge"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message = %q, want it to name %s", res.Message, want)
		}
	}
	if len(seen) != 0 {
		t.Errorf("ran %d suite(s) before refusing the configuration: %+v", len(seen), seen)
	}
	if len(*calls) != 0 {
		t.Errorf("measured something for a repo whose configuration was refused: %+v", *calls)
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-refused:config")
}

// The configuration is read ahead of the docs-only fast path as well: a repo
// whose mutation configuration is addressed to a mechanism that is gone must
// hear so on any merge it makes, not only on one that happens to carry code.
func TestMechanical_RetiredKeyRefusesEvenADocsOnlyMerge(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root := makeDocsOnlyMergeRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-receipt = true\n")
	if !docsOnly(root) {
		t.Fatal("fixture invariant broken: the merge has to stage prose only")
	}

	res := Mechanical(root, passingSuiteRunner())

	if !res.Blocked {
		t.Fatalf("a retired key must refuse a docs-only merge too, got %+v", res)
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-refused:config")
}

// A mutation run costs twenty minutes and a merge whose suite is red is going
// nowhere, so the measurement is the LAST thing the merge gate spends: a red
// suite must never pay for one.
func TestMechanical_RunsTheMeasurementAfterTheSuitesNotBefore(t *testing.T) {
	_, root := mergeStageFixture(t)
	declareMutantsAtMerge(t, root)
	calls := stubMutantsExec(t, nil)

	res := Mechanical(root, func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		return SuiteResult{Passed: false, Output: "test add_works ... FAILED\n"}
	})

	if !res.Blocked {
		t.Fatalf("a red suite must block the merge, got %+v", res)
	}
	if len(*calls) != 0 {
		t.Errorf("measured mutants for a merge whose suite was already red: %+v", *calls)
	}
}

// The docs-only fast path takes no build lock and runs no suite; a mutation
// run is the most expensive thing the gate owns, and a merge whose lane
// carried only prose has nothing mutable in it to measure. This is a real
// MERGE, not a docs-only commit: the fast path is what has to hold here.
func TestMechanical_DocsOnlyMergeNeverMeasures(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root := makeDocsOnlyMergeRepo(t)
	declareMutantsAtMerge(t, root)
	calls := stubMutantsExec(t, nil)

	res := Mechanical(root, passingSuiteRunner())

	if res.Blocked {
		t.Fatalf("a docs-only merge must not block: %s", res.Message)
	}
	if len(*calls) != 0 {
		t.Errorf("a docs-only merge measured mutants: %+v", *calls)
	}
	if !strings.Contains(gateLogText(t, cfgDir), "docs-only-fastpath") {
		t.Errorf("a docs-only merge did not take the fast path:\n%s", gateLogText(t, cfgDir))
	}
	requireNoMutantsMeasurement(t, cfgDir)
}

// makeDocsOnlyMergeRepo merges a lane whose only commit touched markdown, and
// leaves the merge staged: the merge state is the point, so the fixture pins
// it rather than trusting git to have left it behind.
func makeDocsOnlyMergeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	writeMeasureBase(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	trunk := currentBranch(t, root)
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "NOTES.md", "# notes\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane writes prose")
	gitDo(t, root, "checkout", "-q", trunk)
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "-q", "lane")
	requireMergeState(t, root, "MERGE_HEAD")
	return root
}

// The stage reads the same keys `gate mutants run` does, from the same table:
// a mutants-after naming a file that is not there is a refusal, because a hook
// that never ran leaves behind exactly what it exists to reclaim.
func TestMutantsStage_MissingAfterHookIsARefusal(t *testing.T) {
	cfgDir, root := mergeStageFixture(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-merge = true\nmutants-after = \"tools/after.sh\"\n")
	calls := stubMutantsExec(t, nil)

	res := mutantsStage(premergeDisplayName, root)

	if !res.Blocked {
		t.Fatalf("a mutants-after that is not there must refuse, got %+v", res)
	}
	if !strings.Contains(res.Message, filepath.ToSlash("tools/after.sh")) {
		t.Errorf("message = %q, want the path it named", res.Message)
	}
	if len(*calls) != 0 {
		t.Errorf("measured anyway: %+v", *calls)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "after.sh")); err == nil {
		t.Fatal("fixture invariant broken: the hook the test says is missing exists")
	}
	requireMutantsLogLine(t, cfgDir, premergeLogToken, "mutants-refused:config")
}
