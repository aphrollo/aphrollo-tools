package gitx

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// During `git commit -a` the hook's git calls for the repository read the
// index the commit is building, not the stale default one.
func TestGit_ReadsTheIndexAGitCommitDashABuilds(t *testing.T) {
	repo := tddtest.CommitAIndexRepo(t)
	got, err := git(repo, "show", ":f.txt")
	if err != nil || got != "v2\n" {
		t.Fatalf("git show :f.txt = %q (%v), want the staged v2", got, err)
	}
}

// A plain `git commit` in a linked worktree runs the hook with GIT_INDEX_FILE
// naming that worktree's own index. The gate's checkout of HEAD, made with
// `git worktree add`, must not inherit it: git would check the new tree out
// through the lane's index and reset it to HEAD, and the commit then lands
// empty.
func TestGit_WorktreeAddLeavesTheIndexTheHookNamesAlone(t *testing.T) {
	_, lane := tddtest.GoPrimaryWithLane(t)
	tddtest.Write(t, lane, "staged.txt", "staged\n")
	tddtest.GitDo(t, lane, "add", "staged.txt")
	idx, err := git(lane, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	t.Setenv("GIT_INDEX_FILE", filepath.Join(strings.TrimSpace(idx), "index"))

	wt := filepath.Join(t.TempDir(), "gate")
	if out, err := git(lane, "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}

	got, err := git(lane, "diff", "--cached", "--name-only")
	if err != nil || strings.TrimSpace(got) != "staged.txt" {
		t.Fatalf("staged files after worktree add = %q (%v), want staged.txt", got, err)
	}
}
