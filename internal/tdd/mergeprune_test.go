package tdd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pruneRepo builds a main repo on `main` plus two linked worktrees: one on a
// branch that has genuinely landed on main (merged with a real merge commit,
// its own tip now an ANCESTOR of main's, never equal to it), and one on a
// brand-new branch created at main's CURRENT tip with no commits of its own
// — the shape a builder starts a lane from before touching anything.
func pruneRepo(t *testing.T) (mainRepo, mergedWT, freshWT string) {
	t.Helper()
	mainRepo = t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)

	gitDo(t, mainRepo, "branch", "lane/merged")
	mergedWT = filepath.Join(t.TempDir(), "merged")
	gitDo(t, mainRepo, "worktree", "add", "-q", mergedWT, "lane/merged")
	write(t, mergedWT, "landed.go", "package main\n\n// landed\n")
	gitDo(t, mergedWT, "add", "-A")
	gitDo(t, mergedWT, "commit", "-qm", "lane work")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge lane/merged", "lane/merged")

	freshWT = filepath.Join(t.TempDir(), "fresh")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/fresh", freshWT)

	return mainRepo, mergedWT, freshWT
}

// A fresh, unmerged lane sitting at main's own tip survives the post-merge
// sweep; a lane whose branch has actually landed is removed. `git branch
// --merged main` alone would prune BOTH — a branch created minutes earlier
// with no commits of its own trivially satisfies "merged" too, since its tip
// IS main's tip — which is the incident issue #144 records: a fresh lane
// pruned out from under a builder still working in it.
func TestPruneMergedLanesAfterMerge_PrunesLandedWorkSparesAFreshLane(t *testing.T) {
	mainRepo, mergedWT, freshWT := pruneRepo(t)

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/merged's worktree at %s must be pruned (its branch landed on main), got err=%v", mergedWT, err)
	}
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("lane/fresh's worktree at %s must survive (no work of its own yet), got err=%v", freshWT, err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
	if !strings.Contains(out.String(), "lane/merged") {
		t.Fatalf("stdout = %q, want it to name the pruned lane", out.String())
	}
	if errb.String() != "" {
		t.Fatalf("stderr = %q, want a clean sweep", errb.String())
	}
}

// The worktree running the merge itself must never be swept, whatever its
// own branch's merge state — pruning the ground a process is standing on
// would corrupt the very command that just ran.
func TestPruneMergedLanesAfterMerge_NeverPrunesTheExcludedWorktree(t *testing.T) {
	mainRepo, mergedWT, _ := pruneRepo(t)

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, mergedWT, &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("the excluded (current) worktree at %s must survive, got err=%v", mergedWT, err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing pruned — the only merged lane is the excluded one", pruned)
	}
}
