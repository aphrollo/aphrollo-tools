package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaneOf_ReadsTheBranchFromTheCheckoutWithoutSpawningGit(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/lane/shadow-x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LaneOf(filepath.Join(root, "sub", "dir")); got != "lane/shadow-x" {
		t.Errorf("LaneOf(a path inside the checkout) = %q, want lane/shadow-x", got)
	}
	if got := LaneOf(""); got != "" {
		t.Errorf("LaneOf(\"\") = %q, want no lane", got)
	}
}

// During a rebase HEAD is detached; the branch being rebuilt is named by the rebase's own
// head-name file, in the checkout's own git directory, so its commits keep their lane.
func TestLaneOf_ADetachedHeadMidRebaseIsStillTheBranchBeingRebased(t *testing.T) {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		root := t.TempDir()
		gitDir := filepath.Join(root, ".git")
		if err := os.MkdirAll(filepath.Join(gitDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("0123456789012345678901234567890123456789\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, dir, "head-name"), []byte("refs/heads/lane/off-x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := LaneOf(root); got != "lane/off-x" {
			t.Errorf("%s: LaneOf = %q, want lane/off-x", dir, got)
		}
	}
}

func TestLaneOf_ADetachedHeadOutsideARebaseIsNoLane(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("0123456789012345678901234567890123456789\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LaneOf(root); got != "" {
		t.Errorf("LaneOf = %q, want no lane for a plain detached HEAD", got)
	}
}
