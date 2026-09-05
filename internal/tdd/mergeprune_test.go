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

// pruneRepoOnBranch is pruneRepo generalized to an arbitrary trunk name, with
// init.defaultBranch set in the repo's own config so trunk resolution has
// something to find in a repo with no origin remote.
func pruneRepoOnBranch(t *testing.T, trunk string) (mainRepo, mergedWT, freshWT string) {
	t.Helper()
	mainRepo = t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", trunk)
	gitDo(t, mainRepo, "config", "init.defaultBranch", trunk)
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

// A repo whose default branch is NOT "main" (issue #291: the sweep hardcoded
// `rev-parse main` / `--merged main`) must sweep exactly as well as a
// main-default repo — the resolved trunk, not the literal string "main", is
// what decides what counts as landed.
func TestPruneMergedLanesAfterMerge_ResolvesANonMainTrunk(t *testing.T) {
	mainRepo, mergedWT, freshWT := pruneRepoOnBranch(t, "trunk")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/merged's worktree at %s must be pruned on a trunk-default repo too, got err=%v", mergedWT, err)
	}
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("lane/fresh's worktree at %s must survive, got err=%v", freshWT, err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
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

// The main clone is skipped by PATH, never by assuming it holds a branch
// named "main": parked on a branch that has genuinely landed (a real
// ancestor of main, tip distinct from main's own), it must still never be
// swept — `git worktree remove` refuses the working tree a process runs in,
// so the wrong outcome here is a noisy, avoidable error, not corruption, but
// the sweep must not even attempt it.
func TestPruneMergedLanesAfterMerge_NeverPrunesTheMainCloneItself(t *testing.T) {
	mainRepo, _, _ := pruneRepo(t)
	gitDo(t, mainRepo, "checkout", "-q", "-b", "wip")
	write(t, mainRepo, "wip.go", "package main\n\n// wip\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "wip work")
	gitDo(t, mainRepo, "checkout", "-q", "main")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge wip", "wip")
	gitDo(t, mainRepo, "checkout", "-q", "wip")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if errb.String() != "" {
		t.Fatalf("stderr = %q, want no attempt (let alone a failed one) to prune the main clone", errb.String())
	}
	for _, p := range pruned {
		if p.Branch == "wip" {
			t.Fatalf("pruned the main clone's own branch: %+v", pruned)
		}
	}
}

// A worktree `git worktree remove` genuinely refuses — dirty, here, since a
// removal without --force refuses one with modified or untracked files — is
// reported on stderr and left alone. Pressing on to `branch -D` anyway would
// delete the branch out from under a worktree that is STILL THERE; the
// removal error must stop it there. The failure on one lane must not stop
// the sweep from reaching the others.
func TestPruneMergedLanesAfterMerge_RemovalFailureIsReportedAndTheBranchSurvives(t *testing.T) {
	mainRepo, mergedWT, freshWT := pruneRepo(t)
	write(t, mergedWT, "dirty.txt", "uncommitted\n")

	var out, errb bytes.Buffer
	pruned := PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("a worktree git refused to remove must still be on disk, got err=%v", err)
	}
	if !strings.Contains(errb.String(), "lane/merged") {
		t.Fatalf("stderr = %q, want the failed removal reported by lane", errb.String())
	}
	// The propagated error must be `worktree remove`'s own (git's fatal exit
	// 128 on a dirty worktree), never `branch -D`'s (exit 1) from having
	// pressed on to it anyway — that second command failing too, because the
	// worktree is still there holding the branch, is a coincidence of THIS
	// fixture, not a check the code makes; a bare "return err" reporting the
	// wrong command's failure means it read the remove's own error wrong.
	if !strings.Contains(errb.String(), "exit status 128") {
		t.Fatalf("stderr = %q, want it naming the removal's own exit 128, not a later command's", errb.String())
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing — the removal failed", pruned)
	}
	// gitValue fatals the test if the branch no longer resolves — a failed
	// worktree removal must never still delete the branch by pressing on to
	// `branch -D` anyway.
	gitValue(t, mainRepo, "rev-parse", "--verify", "refs/heads/lane/merged")
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("the failure on lane/merged must not stop the sweep from leaving lane/fresh alone, got err=%v", err)
	}
}
