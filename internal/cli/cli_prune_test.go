package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// prunableWorktree builds a temp repo with one linked worktree at the default
// layout (<parent>/.worktrees/<repo>/<slug>) and chdir's to the parent (outside
// the worktree, so the cwd-guard never trips). Returns the repo, the worktree
// path, and the branch checked out there.
func prunableWorktree(t *testing.T) (repo, wt, branch string) {
	t.Helper()
	parent := t.TempDir()
	repo = filepath.Join(parent, "myrepo")
	branch = "feat/x"
	run := func(dir string, args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "init", "-q", "-b", "main")
	run(repo, "config", "user.email", "t@t")
	run(repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", ".")
	run(repo, "commit", "-qm", "seed")
	wt = filepath.Join(parent, ".worktrees", "myrepo", "feat-x")
	run(repo, "worktree", "add", "-q", "-b", branch, wt, "main")
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	return repo, wt, branch
}

// localBranchListedCLI reports whether repo still has a local branch by name
// (this package's own copy of internal/workspace's test helper of the same
// shape — there is no exported seam to share it across packages).
func localBranchListedCLI(t *testing.T, repo, branch string) bool {
	t.Helper()
	err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run()
	return err == nil
}

// `workspace prune <repo> <branch>` is now `remove <repo> <branch>
// --keep-branch`: it removes that one ticket's worktree, keeps the branch,
// and is safe to re-run — the second call (worktree already gone) is still
// exit 0 with a "[skip] ... already gone" receipt, never an error.
func TestRun_Workspace_Prune_Ticket_Idempotent(t *testing.T) {
	repo, wt, branch := prunableWorktree(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "prune", repo, branch}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("prune exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("prune should remove the ticket worktree, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "[removed]") {
		t.Errorf("receipt should report the removed worktree:\n%s", out.String())
	}
	if !localBranchListedCLI(t, repo, branch) {
		t.Errorf("prune's ticket form must keep the local branch %s", branch)
	}

	var out2, errb2 bytes.Buffer
	if code := Run([]string{"workspace", "prune", repo, branch}, strings.NewReader(""), &out2, &errb2); code != 0 {
		t.Fatalf("idempotent re-run exit = %d, want 0\nstderr: %s", code, errb2.String())
	}
	if !strings.Contains(out2.String(), "already gone") {
		t.Errorf("re-run should report the worktree already gone:\n%s", out2.String())
	}
}

// `workspace prune <repo> <branch> --dry` previews without removing, and the
// preview must not claim a branch delete --keep-branch will skip.
func TestRun_Workspace_Prune_Ticket_DryDoesNotRemove(t *testing.T) {
	repo, wt, branch := prunableWorktree(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "prune", repo, branch, "--dry"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("prune --dry exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("--dry must NOT remove the worktree: %v", err)
	}
	if !strings.Contains(out.String(), "would run") {
		t.Errorf("dry-run should preview the removal:\n%s", out.String())
	}
	if strings.Contains(out.String(), "branch -D") {
		t.Errorf("dry-run preview must not claim a branch delete --keep-branch skips:\n%s", out.String())
	}
}

// `workspace prune <repo> <branch> --force` must forward --force through to
// the underlying remove, so a dirty ticket worktree still goes.
func TestRun_Workspace_Prune_Ticket_ForceRemovesADirtyTree(t *testing.T) {
	repo, wt, branch := prunableWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A forced removal of a dirty tree is a discard the git shim's wall refuses;
	// the test models the operator arming the override, so its verdict is the
	// same whether PATH's git is the shim (this box) or real git (CI).
	t.Setenv("APHROLLO_DISCARD", "1")

	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "prune", repo, branch, "--force"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("prune --force exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("--force should remove the dirty ticket worktree, stat err = %v", err)
	}
	if !localBranchListedCLI(t, repo, branch) {
		t.Errorf("prune's ticket form must keep the local branch %s even with --force", branch)
	}
}

// Without --force, `workspace prune <repo> <branch>` on a dirty worktree is
// refused (git's own refusal) and the tree is left intact — the flag must
// not be silently dropped in either direction.
func TestRun_Workspace_Prune_Ticket_WithoutForceRefusesADirtyTree(t *testing.T) {
	repo, wt, branch := prunableWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"workspace", "prune", repo, branch}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("prune without --force on a dirty worktree should not exit 0\nstdout: %s", out.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("a refused prune must leave the dirty worktree intact: %v", err)
	}
}
