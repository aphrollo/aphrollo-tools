package tdd

import (
	"path/filepath"
	"testing"
)

// linkedMutantsJob is the shape the local Go job was in when it did the
// damage: a lane commit, and a mutation worktree LINKED to that repository —
// sharing its refs, its config and its object store.
func linkedMutantsJob(t *testing.T) MutantsJob {
	t.Helper()
	root := makeGoRepo(t)
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "calc.go", "package m\n\nfunc Calc() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")
	tip := gitValue(t, root, "rev-parse", "HEAD")
	worktree := filepath.Join(t.TempDir(), "mutants")
	gitDo(t, root, "worktree", "add", "-q", "--detach", worktree, tip)
	return MutantsJob{
		Schema: StateSchema, Repo: commonGitDir(root), RepoRoot: root, Branch: "lane/x",
		Tip: tip, TipTree: gitValue(t, root, "rev-parse", "HEAD:"),
		BaseRef: "HEAD~1", BaseSHA: gitValue(t, root, "rev-parse", "HEAD~1"),
		Worktree: worktree, TargetDir: filepath.Join(worktree, "target"),
	}
}

// standalone reports git's own answer to "is this its own repository": a
// linked worktree's --git-dir is <common>/worktrees/<name>, a clone's is its
// own --git-common-dir.
func standalone(t *testing.T, dir string) bool {
	t.Helper()
	return gitValue(t, dir, "rev-parse", "--path-format=absolute", "--git-dir") ==
		gitValue(t, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// A mutation run rewrites the tree it runs in, and this repo's own tests are
// gate tests: under mutation they run git init, commit, merge, worktree add
// and config core.bare. Run in a LINKED worktree they reached the repository
// every other worktree resolves its refs through (issue #156), so the Go job
// must not run in one.
func TestGoMutantsTree_ClonesOutOfALinkedWorktree(t *testing.T) {
	j := linkedMutantsJob(t)

	tree, err := goMutantsTree(j)
	if err != nil {
		t.Fatal(err)
	}
	if tree == j.Worktree {
		t.Fatalf("the run tree is still the linked worktree %s", tree)
	}
	if !standalone(t, tree) {
		t.Errorf("%s still shares a git common dir — the run tree must be a repository of its own", tree)
	}
	if head := gitValue(t, tree, "rev-parse", "HEAD"); head != j.Tip {
		t.Errorf("run tree HEAD = %s, want the lane tip %s", head, j.Tip)
	}
}

// The claim in full, spelled as the damage: local main moved to a fake "init"
// commit, the lane branch rewritten, core.bare flipped to true. Every one of
// those is a thing a mutated gate test really did, and none of them may reach
// the repository the lane lives in.
func TestGoMutantsTree_ARewriteInTheRunTreeLeavesTheRealRepositoryAlone(t *testing.T) {
	j := linkedMutantsJob(t)
	tree, err := goMutantsTree(j)
	if err != nil {
		t.Fatal(err)
	}

	// A clone carries no LOCAL config, so the run tree needs the identity a
	// commit demands — exactly what a gate test sets for itself.
	gitDo(t, tree, "config", "user.email", "t@t")
	gitDo(t, tree, "config", "user.name", "t")
	gitDo(t, tree, "checkout", "-q", "-B", "lane/x")
	gitDo(t, tree, "commit", "-q", "--allow-empty", "-m", "init")
	gitDo(t, tree, "config", "core.bare", "true")

	if got := gitValue(t, j.RepoRoot, "rev-parse", "refs/heads/lane/x"); got != j.Tip {
		t.Errorf("the lane branch moved to %s, want it still at %s", got, j.Tip)
	}
	if got := gitValue(t, j.RepoRoot, "config", "--get", "core.bare"); got != "false" {
		t.Errorf("core.bare = %q in the real repository, want \"false\"", got)
	}
}

// The clone costs a checkout, so it is only paid when it buys something: a
// tree that is already its own repository is already isolated.
func TestGoMutantsTree_KeepsAStandaloneCheckoutAsItsOwnRunTree(t *testing.T) {
	j := linkedMutantsJob(t)
	j.Worktree = j.RepoRoot

	tree, err := goMutantsTree(j)
	if err != nil {
		t.Fatal(err)
	}
	if tree != j.RepoRoot {
		t.Errorf("run tree = %s, want the standalone checkout %s unchanged", tree, j.RepoRoot)
	}
}

// The isolation is worth nothing unless the job uses it: gremlins has to be
// spawned in the isolated tree, not in the worktree the job was prepared in.
func TestRunGoMutantsJob_RunsGremlinsOutsideTheLinkedWorktree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	j := linkedMutantsJob(t)
	var ranIn string
	prev := goMutantsJobRunFn
	goMutantsJobRunFn = func(job MutantsJob, outPath string, _ int) int {
		ranIn = job.Worktree
		mustWrite(t, outPath, ciReport)
		return 0
	}
	t.Cleanup(func() { goMutantsJobRunFn = prev })

	RunGoMutantsJob(writeJobFile(t, j))

	if ranIn == "" {
		t.Fatal("gremlins never ran")
	}
	if ranIn == j.Worktree {
		t.Fatalf("gremlins ran in the linked worktree %s", ranIn)
	}
	if !standalone(t, ranIn) {
		t.Errorf("gremlins ran in %s, which still shares a git common dir", ranIn)
	}
}
