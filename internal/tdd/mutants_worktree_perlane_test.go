package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// MutantsWorktreeDir returned one path per REPO, and MutantsTargetDir put the
// build directory inside it. That is warm and cheap while a repo has one lane
// in flight, and destructive the moment it has two: a commit in any lane fires
// the post-commit hook, and the detached run checks that shared worktree out
// to its own tip, under whichever run is still working in it.
//
// Observed in borld with four lanes committing: every lane's run pointed at
// the same D:\Projects\.worktrees\borld\mutants and the same target dir; a run
// for tip 2e7de7b3 died with `ERROR Worker thread failed: The file exists.
// (os error 80)` and was evicted from the job registry by two later lanes;
// another was recorded as exit 4 having reached no verdict. Six attempts over
// four hours produced no receipt, and the receipt is what the merge gate
// consumes — so no lane could merge at all.
//
// Two lanes are two directories. The key is the lane's own checkout, not its
// branch: the branch is renameable and carries path separators, while the
// worktree path is what the run actually operates in. (The BUILD directory is
// the one thing they share — see TestMutantsTargetDir_IsOnePerRepoSharedByEveryLane.)
func TestMutantsWorktreeDir_GivesTwoLanesOfOneRepoTwoDirectories(t *testing.T) {
	root := makeGoRepo(t)
	laneA := addWorktree(t, root, "lane-a")
	laneB := addWorktree(t, root, "lane-b")

	a, b := MutantsWorktreeDir(laneA), MutantsWorktreeDir(laneB)
	if a == b {
		t.Fatalf("both lanes measure in %q — the second run checks the tree out from under the first, and neither reaches a verdict", a)
	}
}

// ...and the directories still live under the repo's ONE mutants root, beside
// the lane worktrees rather than inside a checkout. The build-slot bypass, the
// gc sweep and the primary-checkout guardrail all key on that containment, so
// a per-lane directory that escaped it would trade one defect for three.
func TestMutantsWorktreeDir_KeepsEveryLanesTreeUnderTheRepoMutantsRoot(t *testing.T) {
	root := makeGoRepo(t)
	lane := addWorktree(t, root, "lane-a")

	wantRoot := filepath.Join(filepath.Dir(filepath.Clean(root)), ".worktrees", filepath.Base(filepath.Clean(root)), "mutants")
	for _, dir := range []string{MutantsWorktreeDir(root), MutantsWorktreeDir(lane)} {
		if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(wantRoot)) {
			t.Errorf("MutantsWorktreeDir = %q, want it under the repo's mutants root %q", dir, wantRoot)
		}
	}
}

// The same lane asked twice must answer the same directory: the run resumes
// into a warm tree across commits, and a key that moved would cold-build every
// time and leak a directory per commit.
func TestMutantsWorktreeDir_AnswersTheSameDirectoryForOneLaneTwice(t *testing.T) {
	root := makeGoRepo(t)
	lane := addWorktree(t, root, "lane-a")

	if first, second := MutantsWorktreeDir(lane), MutantsWorktreeDir(lane); first != second {
		t.Errorf("MutantsWorktreeDir = %q then %q — one lane must keep one warm tree", first, second)
	}
}

// addWorktree adds a linked worktree on a new branch and returns its path.
func addWorktree(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	gitDo(t, root, "worktree", "add", "-b", name, dir)
	return dir
}
