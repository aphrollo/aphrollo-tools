package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// A linked worktree's `.git` is a FILE, and an agent keeps whole lane
// checkouts under .claude/worktrees/: neither is a subject of any law.
func TestCheckWalk_SkipsGitFilesAndClaudeWorktrees(t *testing.T) {
	root := t.TempDir()
	allLaw := strings.Replace(docLaw, `include = ["**/*.md"]`, `include = ["**"]`, 1)
	writeLaw(t, root, "doc-names", strings.Replace(allLaw, "%s", "", 1))
	write(t, filepath.Join(root, "task9.txt"), "control\n")
	write(t, filepath.Join(root, "task1", ".git"), "gitdir: /elsewhere\n")
	write(t, filepath.Join(root, ".claude", "worktrees", "lane", "task2.txt"), "a lane checkout\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := strings.Join(res.Lines(), "\n")
	if !strings.Contains(got, "task9.txt") {
		t.Fatalf("control file not judged, the walk is vacuous: %s", got)
	}
	if strings.Contains(got, "task1/.git") || strings.Contains(got, "task2.txt") {
		t.Fatalf("a .git file or .claude/worktrees was walked: %s", got)
	}
}
