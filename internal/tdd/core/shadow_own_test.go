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
