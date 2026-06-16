package workspace

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestCleanup_RemovesWorktreeAndPrunes(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	// cwd is the package source dir (the real repo), not the temp worktree, so the
	// standing-in-it guard does not trip.

	c, err := CleanupPlan(repo, branch, "", false)
	if err != nil {
		t.Fatalf("CleanupPlan: %v", err)
	}
	if c.Worktree != wt {
		t.Errorf("Worktree = %q, want %q", c.Worktree, wt)
	}

	// Dry-run previews without removing.
	var dry, dryErr bytes.Buffer
	if err := c.Run(false, &dry, &dryErr); err != nil {
		t.Fatalf("dry Run: %v", err)
	}
	if !strings.Contains(dry.String(), "worktree remove") || !strings.Contains(dry.String(), "prune") {
		t.Errorf("dry-run should preview remove + prune:\n%s", dry.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("dry-run must not remove the worktree: %v", err)
	}

	// Apply removes it.
	var out, errb bytes.Buffer
	if err := c.Run(true, &out, &errb); err != nil {
		t.Fatalf("apply Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should be gone after cleanup, stat err = %v", err)
	}
	if !strings.Contains(out.String(), "removed worktree") {
		t.Errorf("output missing removal confirmation:\n%s", out.String())
	}
}

func TestCleanup_RefusesToRemoveCwd(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	t.Chdir(wt) // stand inside the worktree we're about to remove
	_ = repo
	_, err := CleanupPlan("", branch, "", false)
	if err == nil || !strings.Contains(err.Error(), "standing in") {
		t.Fatalf("expected a refusal to remove the cwd worktree, got: %v", err)
	}
}

func TestCleanup_MissingWorktree(t *testing.T) {
	repo := initRepo(t)
	if _, err := CleanupPlan(repo, "never-made", "", false); err == nil {
		t.Fatal("expected an error for a missing worktree")
	}
}

func TestPathWithin(t *testing.T) {
	cases := []struct {
		child, parent string
		want          bool
	}{
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/b", true},
		{"/a/bc", "/a/b", false}, // sibling prefix, not a descendant
		{"/x", "/a/b", false},
	}
	for _, c := range cases {
		if got := pathWithin(c.child, c.parent); got != c.want {
			t.Errorf("pathWithin(%q,%q) = %v, want %v", c.child, c.parent, got, c.want)
		}
	}
}
