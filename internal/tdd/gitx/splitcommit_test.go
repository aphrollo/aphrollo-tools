package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// splitGit runs git in dir and returns its trimmed stdout; a failure is fatal.
func splitGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func splitRead(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// splitRepo is a Go repo with one staged test and one staged source file.
func splitRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nfunc TestWidget() {}\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", "widget_test.go", "widget.go")
	return root
}

// The commit carries exactly the named paths' staged content on top of HEAD;
// the rest of the staged change stays staged for the next commit.
func TestCommitStagedSubset_CommitsOnlyTheNamedPathsAndLeavesTheRestStaged(t *testing.T) {
	root := splitRepo(t)
	before := splitGit(t, root, "rev-parse", "HEAD")

	got, err := CommitStagedSubset(root, []string{"widget_test.go"}, "Add the widget test")
	if err != nil {
		t.Fatalf("CommitStagedSubset: %v", err)
	}

	if head := splitGit(t, root, "rev-parse", "HEAD"); head != got {
		t.Fatalf("HEAD = %s, want the returned commit %s", head, got)
	}
	if parent := splitGit(t, root, "rev-parse", "HEAD^"); parent != before {
		t.Fatalf("parent = %s, want the previous HEAD %s", parent, before)
	}
	if files := splitGit(t, root, "show", "--name-only", "--format=", "HEAD"); files != "widget_test.go" {
		t.Fatalf("commit files = %q, want only widget_test.go", files)
	}
	if staged := splitGit(t, root, "diff", "--cached", "--name-only"); staged != "widget.go" {
		t.Fatalf("still staged = %q, want only widget.go", staged)
	}
	if msg := splitGit(t, root, "log", "-1", "--format=%B"); msg != "Add the widget test" {
		t.Fatalf("message = %q", msg)
	}
}

// Nothing in the working tree moves: an unstaged edit on top of a staged
// file survives byte for byte, and the commit holds the STAGED blob.
func TestCommitStagedSubset_NeverTouchesTheWorkingTree(t *testing.T) {
	root := splitRepo(t)
	staged := splitRead(t, root, "widget_test.go")
	unstaged := staged + "// edited after staging\n"
	write(t, root, "widget_test.go", unstaged)
	write(t, root, "scratch.txt", "untracked\n")

	if _, err := CommitStagedSubset(root, []string{"widget_test.go"}, "Add the widget test"); err != nil {
		t.Fatalf("CommitStagedSubset: %v", err)
	}

	if got := splitRead(t, root, "widget_test.go"); got != unstaged {
		t.Fatalf("working file = %q, want the unstaged edit kept: %q", got, unstaged)
	}
	if got := splitRead(t, root, "scratch.txt"); got != "untracked\n" {
		t.Fatalf("untracked file = %q, want it untouched", got)
	}
	if got := splitGit(t, root, "show", "HEAD:widget_test.go") + "\n"; got != staged {
		t.Fatalf("committed blob = %q, want the staged content %q", got, staged)
	}
}

// A staged deletion of a named path is part of the subset.
func TestCommitStagedSubset_CarriesAStagedDeletion(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "old_test.go", "package m\n")
	gitDo(t, root, "add", "old_test.go")
	gitDo(t, root, "commit", "-qm", "add old test")
	gitDo(t, root, "rm", "-q", "old_test.go")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", "widget.go")

	if _, err := CommitStagedSubset(root, []string{"old_test.go"}, "Drop the old test"); err != nil {
		t.Fatalf("CommitStagedSubset: %v", err)
	}

	if files := splitGit(t, root, "show", "--name-status", "--format=", "HEAD"); files != "D\told_test.go" {
		t.Fatalf("commit = %q, want the deletion of old_test.go", files)
	}
	if staged := splitGit(t, root, "diff", "--cached", "--name-only"); staged != "widget.go" {
		t.Fatalf("still staged = %q, want only widget.go", staged)
	}
}

// A path named with glob characters is a literal file name, never a pattern
// that could sweep another staged file into the commit.
func TestCommitStagedSubset_TreatsPathsAsLiterals(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "a_test.go", "package m\n")
	write(t, root, "b_test.go", "package m\n")
	gitDo(t, root, "add", "a_test.go", "b_test.go")

	if _, err := CommitStagedSubset(root, []string{"*_test.go"}, "glob"); err == nil {
		t.Fatal("a glob names no staged path and must be refused, not expanded")
	}
	if staged := splitGit(t, root, "diff", "--cached", "--name-only"); staged != "a_test.go\nb_test.go" {
		t.Fatalf("still staged = %q, want both files untouched", staged)
	}
}

// Every refusal leaves HEAD and the index exactly where they were.
func TestCommitStagedSubset_RefusalsLeaveHeadAndIndexAlone(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		msg   string
	}{
		{"no paths", nil, "message"},
		{"blank message", []string{"widget_test.go"}, "  \n"},
		{"path with no staged change", []string{"go.mod"}, "message"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := splitRepo(t)
			head := splitGit(t, root, "rev-parse", "HEAD")
			index := splitGit(t, root, "write-tree")

			if _, err := CommitStagedSubset(root, tc.paths, tc.msg); err == nil {
				t.Fatal("want a refusal")
			}
			if got := splitGit(t, root, "rev-parse", "HEAD"); got != head {
				t.Fatalf("HEAD moved to %s", got)
			}
			if got := splitGit(t, root, "write-tree"); got != index {
				t.Fatalf("index changed: %s -> %s", index, got)
			}
		})
	}
}

// A repo with no commit has no HEAD to build on.
func TestCommitStagedSubset_RefusesARepoWithoutACommit(t *testing.T) {
	root := t.TempDir()
	gitDo(t, root, "init", "-q")
	write(t, root, "a_test.go", "package m\n")
	gitDo(t, root, "add", "a_test.go")

	if _, err := CommitStagedSubset(root, []string{"a_test.go"}, "first"); err == nil {
		t.Fatal("a repo with no HEAD must be refused")
	}
}

// The scratch index is removed whatever the outcome.
func TestCommitStagedSubset_LeavesNoScratchIndexBehind(t *testing.T) {
	root := splitRepo(t)
	if _, err := CommitStagedSubset(root, []string{"widget_test.go"}, "Add the widget test"); err != nil {
		t.Fatalf("CommitStagedSubset: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(root, ".git", "aphrollo-split-index-*"))
	if len(left) != 0 {
		t.Fatalf("scratch index left behind: %v", left)
	}
}

// gitApplyIndex lands a patch in the worktree's index as well as its files,
// and reports a patch that does not apply.
func TestGitApplyIndex_StagesThePatchAndReportsAFailure(t *testing.T) {
	src := splitRepo(t)
	patch := splitGit(t, src, "diff", "--cached", "--no-renames", "--", "widget_test.go") + "\n"
	dst := makeGoRepo(t)

	if err := gitApplyIndex(dst, patch); err != nil {
		t.Fatalf("gitApplyIndex: %v", err)
	}
	if staged := splitGit(t, dst, "diff", "--cached", "--name-only"); staged != "widget_test.go" {
		t.Fatalf("staged = %q, want widget_test.go", staged)
	}

	if err := gitApplyIndex(dst, patch); err == nil {
		t.Fatal("applying the same new-file patch twice must fail")
	}
}
