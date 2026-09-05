package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// localBranchListed reports whether the repo still has a local branch by name.
func localBranchListed(t *testing.T, repo, branch string) bool {
	t.Helper()
	err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run()
	return err == nil
}

// TestRemove_DeletesWorktreeAndBranch is the happy path: a prepared worktree and
// its local branch are both gone after a single remove.
func TestRemove_DeletesWorktreeAndBranch(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	if !localBranchListed(t, repo, branch) {
		t.Fatalf("precondition: branch %s should exist before remove", branch)
	}

	cmd, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatalf("RemovePlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := cmd.Run(&out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should be gone, stat err = %v", err)
	}
	if localBranchListed(t, repo, branch) {
		t.Errorf("local branch %s should be deleted", branch)
	}
	if !strings.Contains(out.String(), "[removed]") {
		t.Errorf("receipt should report what was removed:\n%s", out.String())
	}
}

// TestRemove_IdempotentReRun is the core guarantee: a second remove of the same
// already-gone worktree+branch is a no-op success ([skip], no error), so the
// cleanup path can re-run on redelivery without wedging.
func TestRemove_IdempotentReRun(t *testing.T) {
	repo, wt, branch := preparedRepo(t)

	cmd, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatalf("RemovePlan: %v", err)
	}
	var out1, err1 bytes.Buffer
	if err := cmd.Run(&out1, &err1); err != nil {
		t.Fatalf("first Run: %v\n%s", err, err1.String())
	}

	// Second run: everything is already gone. Must succeed and skip, not error.
	cmd2, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatalf("RemovePlan (2): %v", err)
	}
	var out2, err2 bytes.Buffer
	if err := cmd2.Run(&out2, &err2); err != nil {
		t.Fatalf("idempotent re-run must succeed, got: %v\n%s", err, err2.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should still be gone after re-run, stat err = %v", err)
	}
	if !strings.Contains(out2.String(), "[skip]") {
		t.Errorf("re-run should report skips for the already-gone worktree+branch:\n%s", out2.String())
	}
}

// TestRemove_RefusesCwd keeps the standing-in-it guard: removing the worktree the
// caller is in is refused up front (git's own error is cryptic).
func TestRemove_RefusesCwd(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	t.Chdir(wt)
	if _, err := RemovePlan(repo, branch, ""); err == nil {
		t.Fatal("RemovePlan should refuse to remove the worktree the caller stands in")
	}
}

// TestRemove_KeepBranchLeavesTheBranch: with KeepBranch set, the worktree still
// goes but the local branch survives; without it (the default) the branch is
// deleted, as TestRemove_DeletesWorktreeAndBranch already pins.
func TestRemove_KeepBranchLeavesTheBranch(t *testing.T) {
	repo, wt, branch := preparedRepo(t)

	cmd, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatalf("RemovePlan: %v", err)
	}
	cmd.KeepBranch = true
	var out, errb bytes.Buffer
	if err := cmd.Run(&out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should be gone, stat err = %v", err)
	}
	if !localBranchListed(t, repo, branch) {
		t.Errorf("--keep-branch should leave local branch %s in place", branch)
	}
}

// TestRemove_ForceRemovesADirtyWorktree: with Force set, a dirty worktree
// still goes — the flag reaches `git worktree remove --force`.
func TestRemove_ForceRemovesADirtyWorktree(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	dirty(t, wt)
	// A forced removal of a dirty tree is a discard the git shim's wall refuses;
	// the test models the operator arming the override, so its verdict is the
	// same whether PATH's git is the shim (this box) or real git (CI).
	t.Setenv("APHROLLO_DISCARD", "1")

	cmd, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatalf("RemovePlan: %v", err)
	}
	cmd.Force = true
	var out, errb bytes.Buffer
	if err := cmd.Run(&out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("--force should remove a dirty worktree, stat err = %v", err)
	}
}

// TestRemove_WithoutForceRefusesADirtyWorktree: the default (no Force) leaves
// a dirty worktree in place — git itself refuses, and the caller sees why.
func TestRemove_WithoutForceRefusesADirtyWorktree(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	dirty(t, wt)

	cmd, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatalf("RemovePlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := cmd.Run(&out, &errb); err == nil {
		t.Fatal("Run should refuse a dirty worktree without --force")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("a refused remove must leave the worktree intact: %v", err)
	}
}

// TestRemove_DisplayReflectsKeepBranch: the dry-run preview must not claim a
// branch delete it will not perform — Display() is read AFTER KeepBranch is
// set, so it has to compute the line then, not bake a stale one in at plan
// time.
func TestRemove_DisplayReflectsKeepBranch(t *testing.T) {
	repo, _, branch := preparedRepo(t)

	cmd, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatalf("RemovePlan: %v", err)
	}
	cmd.KeepBranch = true
	if strings.Contains(cmd.Display(), "branch -D") {
		t.Errorf("Display() with KeepBranch should not mention deleting the branch: %q", cmd.Display())
	}
}
