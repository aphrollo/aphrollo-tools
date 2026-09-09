package tdd

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// A lane that lands through the PR verb never fires the pre-merge-commit
// hook: GitHub makes the commit, and by the time the local box sees it the
// merge is a fact. GatePRMerge is what closes that: it builds the merge the
// PR is about to make, locally and in a throwaway checkout, and runs the SAME
// Mechanical stage the local merge runs — before the lane lands, never after.
// What these tests pin is not the measurement (mutants_stage_test owns that)
// but WHICH merges it judges, WHERE it judges them, and that nothing
// inconclusive is ever read as a pass.

// gateRun is one suite the gate asked for, and the directory it asked for it
// in — the second half matters as much as the first, because a gate that ran
// the suite in the LANE worktree judged the lane, not the merge.
type gateRun struct {
	Runner Runner
	Dir    string
}

// recordRuns answers every suite with the same result and records the call.
func recordRuns(seen *[]gateRun, result SuiteResult) SuiteRunner {
	return func(r Runner, dir string) SuiteResult {
		*seen = append(*seen, gateRun{Runner: r, Dir: dir})
		return result
	}
}

// prGateLane is a lane worktree ready to be merged: trunk has moved on since
// the lane forked, and the lane itself is checked out. It is one checkout
// rather than two because git resolves a branch tip the same way either way,
// and the gate builds its own checkout regardless.
func prGateLane(t *testing.T) (root, trunk string) {
	t.Helper()
	root, trunk = makeForkedRepo(t)
	write(t, root, "crates/a/src/other.rs", "pub fn other() -> i32 { 7 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk moves on")
	gitDo(t, root, "checkout", "-q", "lane")
	return root, trunk
}

// declareMutantsAtMergeCommitted puts the declaration in the TREE, not just
// the working copy: the gate judges a checkout built from a commit, so a key
// only ever written to disk would not reach the tree that is measured.
func declareMutantsAtMergeCommitted(t *testing.T, root string) {
	t.Helper()
	declareMutantsAtMerge(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "declare mutants-at-merge")
}

// A repo that declares no mutants-at-merge must merge exactly as it does
// today: no throwaway checkout, no suite, no cost at all.
func TestGatePRMerge_UndeclaredRepoRunsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)

	var seen []gateRun
	if err := GatePRMerge(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard); err != nil {
		t.Fatalf("a repo that declares nothing must merge unchanged: %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("ran a suite for a repo that declares nothing: %+v", seen)
	}
}

// The refusal that is the whole point: the merged tree is red, so the PR does
// not land. The suite runs in a checkout of the GATE's own making, carrying
// both sides of the merge — the lane's change and trunk's.
func TestGatePRMerge_RefusesRedMergedTreeBeforeItLands(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := prGateLane(t)
	declareMutantsAtMergeCommitted(t, root)

	var seen []gateRun
	err := GatePRMerge(root, recordRuns(&seen, SuiteResult{Passed: false, Output: "merged tree is red"}), io.Discard)

	if err == nil {
		t.Fatal("a red merged tree must refuse the merge")
	}
	if !strings.Contains(err.Error(), "merged tree is red") {
		t.Errorf("the refusal must carry what the merged tree said, got: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("the gate refused without running anything")
	}
	for _, r := range seen {
		if underDir(t, r.Dir, root) {
			t.Fatalf("the gate judged the lane worktree %s, not a merge of it: %+v", root, r)
		}
	}
}

// A merge that cannot be built locally is not a pass. Nothing was measured,
// nothing was proven, so the lane is refused with the reason rather than
// waved through on the strength of a check that never ran.
func TestGatePRMerge_RefusesWhenTheMergeCannotBeBuilt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := makeForkedRepo(t)
	// Trunk changes the SAME line the lane did: the merge conflicts, so no
	// merged tree exists to judge.
	write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a * b }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk touches the same line")
	gitDo(t, root, "checkout", "-q", "lane")
	declareMutantsAtMergeCommitted(t, root)

	var seen []gateRun
	err := GatePRMerge(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard)

	if err == nil {
		t.Fatalf("a merge that could not be built must be refused, not allowed (trunk %s)", trunk)
	}
	if len(seen) != 0 {
		t.Errorf("nothing can be judged when the merge does not exist, but a suite ran: %+v", seen)
	}
}

// The green path, end to end: a merged tree whose suites pass and whose
// mutants are all caught lands. The measurement runs in the gate's own
// checkout, once, and on the merged tree.
func TestGatePRMerge_GreenMergedTreeLandsAfterOneMeasurement(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	t.Cleanup(setMutantsJobsForTest(1, "pinned"))
	root, _ := prGateLane(t)
	declareMutantsAtMergeCommitted(t, root)
	// The checkout is torn down before GatePRMerge returns, so what it held
	// is read while the measurement is standing in it — afterwards there is
	// nothing left to look at, which is the point of a throwaway.
	var lane, trunkSide string
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		lane = readFileString(t, filepath.Join(c.Dir, "crates", "a", "src", "lib.rs"))
		trunkSide = readFileString(t, filepath.Join(c.Dir, "crates", "a", "src", "other.rs"))
		writeOutcomesIn(t, argvValueOf(t, c.Argv, "--output"), MutantOutcome{
			File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace - with +", Package: "a", Status: "caught"})
		return 0, nil
	})

	var seen []gateRun
	if err := GatePRMerge(root, recordRuns(&seen, SuiteResult{Passed: true}), io.Discard); err != nil {
		t.Fatalf("a green merged tree must land: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("ran the mutation tool %d time(s), want exactly one measurement", len(*calls))
	}
	if underDir(t, (*calls)[0].Dir, root) {
		t.Fatalf("measured the lane worktree %s rather than a merge of it", root)
	}
	// The tree that was measured is the MERGE: the lane's own change to the
	// crate, and trunk's separate change since the lane forked.
	if !strings.Contains(lane, "a - b") {
		t.Errorf("the measured tree is missing the lane's own change: %s", lane)
	}
	if !strings.Contains(trunkSide, "pub fn other") {
		t.Errorf("the measured tree is missing trunk's change since the fork: %s", trunkSide)
	}
}

// underDir reports whether path is dir or lives inside it, as the filesystem
// sees them: a temp dir reaches this side through a symlink on macOS and an
// 8.3 short name on Windows, so string equality would answer the wrong
// question.
func underDir(t *testing.T, path, dir string) bool {
	t.Helper()
	rel, err := filepath.Rel(realPath(path), realPath(dir))
	if err != nil {
		return strings.EqualFold(filepath.Clean(path), filepath.Clean(dir))
	}
	// dir is at or above path exactly when getting from path to dir is all
	// "..": any named component means they sit on different branches.
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part != ".." && part != "." {
			return false
		}
	}
	return true
}

// realPath resolves what it can and hands back the input when it cannot: a
// path that no longer exists (a torn-down checkout) is still comparable.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}
