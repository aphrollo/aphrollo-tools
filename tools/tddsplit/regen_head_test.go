package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed tree -regen compares against must come out of the repository
// without registering a worktree in it: a registration is state of the real
// repository that a canary watching it, or a second tool listing worktrees,
// sees while the comparison runs (#1072, #1074, #1076).
func TestCheckoutHEAD_WritesTheCommittedTreeAndRegistersNoWorktree(t *testing.T) {
	repo := regenFixtureRepo(t)
	// An uncommitted edit must not show: the dir holds HEAD, not the tree.
	writeTree(t, repo, map[string]string{"p/b.go": "package p\n\nfunc Edited() {}\n"})
	dir := t.TempDir()

	if err := checkoutHEAD(repo, dir); err != nil {
		t.Fatalf("checkoutHEAD: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "p", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "Edited") {
		t.Errorf("p/b.go holds the working-tree edit, want the committed content:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "p", "low", "export.go")); err != nil {
		t.Errorf("a committed generated file is missing from the checkout: %v", err)
	}
	if list := git(t, repo, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("worktree registrations after checkoutHEAD:\n%s\nwant only the repository's own", list)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "worktrees")); err == nil {
		t.Errorf(".git/worktrees exists: a worktree was registered and left its admin dir")
	}
	if status := git(t, repo, "status", "--porcelain"); !strings.Contains(status, "p/b.go") || strings.Contains(status, "??") {
		t.Errorf("status = %q: the index or tree was disturbed", status)
	}
}
