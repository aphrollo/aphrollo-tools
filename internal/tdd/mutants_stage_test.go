package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pre-merge stage is where a mutation measurement finally judges the tree
// that is about to land. What it must get right is not the measurement — the
// library owns that — but WHEN it runs and WHAT it hands back: a repo that
// declared nothing is never measured, a repo that declared a retired key is
// refused before anything expensive starts, and a measurement runs only after
// the suites have already proved the merged tree green.

// makeMergeInProgressRepo builds the exact state pre-merge-commit fires in: a
// trunk that has moved on since the lane forked, the lane merged into it with
// --no-commit, so HEAD is still trunk and the merge result exists only in the
// index and the working tree. The lane changes one crate source and trunk
// changes another, so a base taken at HEAD and a base taken at the merge base
// select different files.
func makeMergeInProgressRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	writeMeasureBase(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	trunk := strings.TrimSpace(gitOutT(t, root, "rev-parse", "--abbrev-ref", "HEAD"))
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a - b }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "crates/a/src/other.rs", "pub fn other() -> i32 { 7 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk moves on")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "-q", "lane")
	return root
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

// mutantsLogLine is the gate.log line carrying one verdict, whole.
func mutantsLogLine(t *testing.T, cfgDir, verdict string) string {
	t.Helper()
	text := gateLogText(t, cfgDir)
	for line := range strings.SplitSeq(text, "\n") {
		if e, ok := parseGateLine(line); ok && e.verdict == verdict {
			return line
		}
	}
	t.Fatalf("no gate.log line with verdict %q, got:\n%s", verdict, text)
	return ""
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
	line := mutantsLogLine(t, cfgDir, "mutants-skipped:not-declared")
	if fields := strings.Fields(line); len(fields) < 5 || fields[1] != premergeLogToken {
		t.Errorf("logged under stage %q, want %q so the merge gate's own row counts it: %s",
			strings.Fields(line)[1], premergeLogToken, line)
	}
	if !strings.Contains(line, " mutants ") {
		t.Errorf("log line names no command: %s", line)
	}
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

// The gate failing on its own inputs says which input. A clean automerge has
// no MERGE_HEAD yet and only GIT_REFLOG_ACTION names the branch coming in, so
// a stage that can resolve neither cannot know what it would be measuring.
func TestMutantsStage_NoLaneTipRefusesNamingBothSignals(t *testing.T) {
	root, _ := measureFixture(t, laneSource)
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
	_, root := mergeStageFixture(t)
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
// run is the most expensive thing the gate owns, and a merge carrying only
// prose has nothing mutable in it to measure.
func TestMechanical_DocsOnlyMergeNeverMeasures(t *testing.T) {
	_, root := mergeStageFixture(t)
	declareMutantsAtMerge(t, root)
	gitDo(t, root, "reset", "-q")
	write(t, root, "NOTES.md", "# notes\n")
	gitDo(t, root, "add", "NOTES.md")
	calls := stubMutantsExec(t, nil)

	res := Mechanical(root, passingSuiteRunner())

	if res.Blocked {
		t.Fatalf("a docs-only merge must not block: %s", res.Message)
	}
	if len(*calls) != 0 {
		t.Errorf("a docs-only merge measured mutants: %+v", *calls)
	}
}

// The stage reads the same keys `gate mutants run` does, from the same table:
// a mutants-after naming a file that is not there is a refusal, because a hook
// that never ran leaves behind exactly what it exists to reclaim.
func TestMutantsStage_MissingAfterHookIsARefusal(t *testing.T) {
	_, root := mergeStageFixture(t)
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
}
