package tdd

import (
	"path/filepath"
	"testing"
)

// The run tree is cloned from the PRIMARY checkout, and the primary is on
// main: it has never had the lane's branch checked out, and the lane's commit
// is reachable from no ref the primary itself carries. A clone that copied
// objects would therefore not have the tip it is asked to check out, and the
// post-commit run would die on every lane commit before measuring anything.
//
// It does not, because the clone is `--shared`: the lane worktree and the
// primary share one object store, and the clone reads through an alternate
// rather than copying. This pins that, with the topology the failure needs —
// the commit made in a LINKED worktree on its own branch, not on the primary.
func TestGoMutantsTree_ChecksOutATipOnlyTheLaneWorktreeHasCommitted(t *testing.T) {
	primary := makeGoRepo(t)
	gitDo(t, primary, "branch", "-M", "main")

	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, primary, "worktree", "add", "-q", "-b", "lane/only-here", lane)
	write(t, lane, "calc.go", "package m\n\nfunc Calc() int { return 1 }\n")
	gitDo(t, lane, "add", "-A")
	gitDo(t, lane, "commit", "-qm", "work only this lane has")
	tip := gitValue(t, lane, "rev-parse", "HEAD")

	// The primary is still on main and does not carry that commit.
	if head := gitValue(t, primary, "rev-parse", "HEAD"); head == tip {
		t.Fatal("the primary is at the lane tip, so this test proves nothing about objects it does not have")
	}

	worktree := filepath.Join(t.TempDir(), "mutants")
	gitDo(t, primary, "worktree", "add", "-q", "--detach", worktree, tip)
	j := MutantsJob{
		Schema: StateSchema, Repo: commonGitDir(lane), RepoRoot: lane, Branch: "lane/only-here",
		Tip: tip, TipTree: gitValue(t, lane, "rev-parse", "HEAD:"),
		BaseRef: "main", BaseSHA: gitValue(t, primary, "rev-parse", "HEAD"),
		Worktree: worktree, TargetDir: filepath.Join(worktree, "target"),
	}

	tree, err := goMutantsTree(j)
	if err != nil {
		t.Fatalf("goMutantsTree: %v — the run tree could not be built from a tip only the lane has", err)
	}
	if head := gitValue(t, tree, "rev-parse", "HEAD"); head != tip {
		t.Errorf("run tree HEAD = %s, want the lane tip %s", head, tip)
	}
	if !standalone(t, tree) {
		t.Errorf("%s still shares a git common dir — the run tree must be a repository of its own", tree)
	}
}
