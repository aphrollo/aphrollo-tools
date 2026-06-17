package tdd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// preEditJSON builds a PreToolUse payload naming a single edited file.
func preEditJSON(t *testing.T, tool, filePath, session string) []byte {
	t.Helper()
	payload := map[string]any{
		"tool_name":  tool,
		"session_id": session,
		"tool_input": map[string]any{"file_path": filePath},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// commitInitial turns a fresh git dir into a repo with one commit, so a
// worktree can be added against it.
func commitInitial(t *testing.T, repo string) {
	t.Helper()
	write(t, repo, "main.go", "package main\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-q", "-m", "init")
}

func TestSameGitDir(t *testing.T) {
	// Main clone: git reports the same dir for --git-dir and --git-common-dir.
	if !sameGitDir("/repo", ".git", ".git") {
		t.Error("main clone (relative .git == .git) should be sameGitDir=true")
	}
	if !sameGitDir("/repo", "/repo/.git", "/repo/.git") {
		t.Error("main clone (abs equal) should be sameGitDir=true")
	}
	// Linked worktree: git-dir is <common>/worktrees/<name>, differs from common.
	if sameGitDir("/repo/.worktrees/x", "/main/.git/worktrees/x", "/main/.git") {
		t.Error("linked worktree should be sameGitDir=false")
	}
}

func TestWorktreeAdvisory_MainCloneWarnsOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	commitInitial(t, repo)

	file := filepath.Join(repo, "main.go")
	const sess = "wt-sess"
	d := WorktreeAdvisory(preEditJSON(t, "Edit", file, sess))
	if d.Action != Warn || !strings.Contains(d.Reason, "worktree") {
		t.Fatalf("main-clone edit should Warn about worktree, got %+v", d)
	}
	// A second edit in the same session is silent — the warning fires once.
	if d2 := WorktreeAdvisory(preEditJSON(t, "Edit", file, sess)); d2.Action != Allow {
		t.Fatalf("second main-clone edit should Allow (warn once), got %+v", d2)
	}
}

func TestWorktreeAdvisory_LinkedWorktreeSilent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	commitInitial(t, repo)

	wt := filepath.Join(t.TempDir(), "wt")
	gitDo(t, repo, "worktree", "add", "-q", wt)
	file := filepath.Join(wt, "main.go")
	if d := WorktreeAdvisory(preEditJSON(t, "Edit", file, "wt-sess2")); d.Action != Allow {
		t.Fatalf("edit inside a linked worktree should Allow, got %+v", d)
	}
}

func TestWorktreeAdvisory_NonGitAllows(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	file := filepath.Join(t.TempDir(), "loose.go") // not under any git repo
	if d := WorktreeAdvisory(preEditJSON(t, "Edit", file, "s")); d.Action != Allow {
		t.Fatalf("edit outside any git repo should Allow, got %+v", d)
	}
}

func TestWorktreeAdvisory_NonEditToolAllows(t *testing.T) {
	if d := WorktreeAdvisory(preEditJSON(t, "Read", "/x/y.go", "s")); d.Action != Allow {
		t.Fatalf("non-edit tool should Allow, got %+v", d)
	}
}
