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
