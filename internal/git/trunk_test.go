package git

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestTrunk_TheRemotesDefaultBranchWinsAndCostsNoSpawn(t *testing.T) {
	origin := repoWithCommit(t)
	// The clone's origin/HEAD names what the remote calls its default; a local
	// init.defaultBranch that disagrees must not outrank it.
	gitT(t, origin, "branch", "-m", "main", "develop")
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, origin, "clone", "-q", origin, clone)
	gitT(t, clone, "config", "init.defaultBranch", "main")
	gitT(t, clone, "branch", "main")
	c := mustNew(t, clone)
	if got := c.Trunk(); got != "origin/develop" {
		t.Errorf("Trunk = %q, want origin/develop", got)
	}
	if c.Spawns() != 0 {
		t.Errorf("%d spawns reading origin/HEAD", c.Spawns())
	}
}

func TestTrunk_ConfiguredDefaultBranchCountsOnlyWhenItResolves(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "branch", "-m", "main", "trunk")
	gitT(t, dir, "config", "init.defaultBranch", "trunk")
	if got := mustNew(t, dir).Trunk(); got != "trunk" {
		t.Errorf("Trunk = %q, want trunk", got)
	}

	gitT(t, dir, "config", "init.defaultBranch", "vanished")
	gitT(t, dir, "branch", "master")
	if got := mustNew(t, dir).Trunk(); got != "master" {
		t.Errorf("Trunk with an unresolvable default = %q, want master", got)
	}
}

func TestTrunk_ResolvesABranchThatLivesInPackedRefs(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "branch", "-m", "main", "trunk")
	gitT(t, dir, "config", "init.defaultBranch", "trunk")
	gitT(t, dir, "pack-refs", "--all", "--prune")
	if got := mustNew(t, dir).Trunk(); got != "trunk" {
		t.Errorf("Trunk = %q, want trunk from packed-refs", got)
	}
}

func TestTrunk_NeverNamesABranchTheRepositoryDoesNotHave(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "branch", "-m", "main", "work")
	c := mustNew(t, dir)
	if got := c.Trunk(); got != "" {
		t.Errorf("Trunk = %q in a repository with only branch work, want none rather than a guess", got)
	}
}

func TestTrunk_ANameThatClimbsOutOfRefsNamesNothing(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "branch", "-m", "main", "work")
	// refs/../HEAD is the repository's HEAD file, which does hold a commit; git
	// refuses it as a ref name.
	gitT(t, dir, "config", "init.defaultBranch", "../HEAD")
	if got := mustNew(t, dir).Trunk(); got != "" {
		t.Errorf("Trunk = %q, want none", got)
	}
}

func TestTrunk_TheConventionalNamesComeLast(t *testing.T) {
	dir := repoWithCommit(t)
	// A box may configure a default branch of its own; an empty one here outranks it.
	gitT(t, dir, "config", "init.defaultBranch", "")
	gitT(t, dir, "branch", "master")
	// Both exist and nothing names trunk: main is the first conventional name.
	if got := mustNew(t, dir).Trunk(); got != "main" {
		t.Errorf("Trunk = %q, want main", got)
	}
	gitT(t, dir, "checkout", "-q", "master")
	gitT(t, dir, "branch", "-D", "main")
	if got := mustNew(t, dir).Trunk(); got != "master" {
		t.Errorf("Trunk = %q, want master", got)
	}
}

// The per-edit facts the gate reads in internal/tdd/postedit today, each one
// asked of git there: the repository root (RepoRoot), whether this is a linked
// worktree (sameGitDir of --git-dir and --git-common-dir), whether the repo has
// a linked worktree at all (hasLinkedWorktree), the current branch
// (`rev-parse --abbrev-ref HEAD`), a merge in progress (mergeInProgressRef),
// the staged files (stagedFiles), whether the edited file differs from HEAD or
// is untracked (editedLinesPatch's `diff HEAD` and `ls-files --others`), and
// trunk (TrunkBranch). Together they were ten or more git children per edit.
func TestClient_AnswersEveryPerEditFactTheGateReadsWithOneSpawn(t *testing.T) {
	origin := repoWithCommit(t)
	main := filepath.Join(t.TempDir(), "main")
	gitT(t, origin, "clone", "-q", origin, main)
	lane := filepath.Join(t.TempDir(), "lane")
	gitT(t, main, "worktree", "add", "-q", "-b", "lane/x", lane)
	write(t, lane, "pkg/keep.txt", "k\n")
	gitT(t, lane, "add", "pkg/keep.txt")
	gitT(t, lane, "commit", "-q", "-m", "pkg")
	write(t, lane, "a.txt", "edited\n")
	write(t, lane, "staged.txt", "s\n")
	gitT(t, lane, "add", "staged.txt")
	write(t, lane, "fresh.txt", "f\n")
	c := mustNew(t, filepath.Join(lane, "pkg"))
	st, err := c.Status("edit-1")
	if err != nil {
		t.Fatal(err)
	}
	head, err := c.Head()
	if err != nil {
		t.Fatal(err)
	}
	worktrees, err := c.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	edited, editedKnown := st.Entry("a.txt")
	fresh, freshKnown := st.Entry("fresh.txt")

	facts := map[string]any{
		"root is the lane":      sameDir(t, c.Root(), lane),
		"linked worktree":       c.IsLinkedWorktree(),
		"has a linked worktree": len(worktrees) > 1,
		"branch":                head.Branch,
		"status branch":         st.Branch.Head,
		"merge in progress":     c.MergeInProgress(),
		"staged":                st.StagedPaths(),
		"edited differs":        editedKnown && edited.Unstaged(),
		"new file untracked":    freshKnown && fresh.Kind == Untracked,
		"trunk":                 c.Trunk(),
	}
	want := map[string]any{
		"root is the lane":      true,
		"linked worktree":       true,
		"has a linked worktree": true,
		"branch":                "lane/x",
		"status branch":         "lane/x",
		"merge in progress":     "",
		"staged":                []string{"staged.txt"},
		"edited differs":        true,
		"new file untracked":    true,
		"trunk":                 "origin/main",
	}
	if !reflect.DeepEqual(facts, want) {
		t.Errorf("facts = %v\nwant    %v", facts, want)
	}
	if c.Spawns() != 1 {
		t.Errorf("Spawns = %d for the per-edit facts, want 1 (the status call)", c.Spawns())
	}
}

func TestTrunk_AMissIsAskedAgainSoARemoteThatNamesItsDefaultLaterIsSeen(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "branch", "-m", "main", "work")
	c := mustNew(t, dir)
	if got := c.Trunk(); got != "" {
		t.Fatalf("Trunk = %q before the remote names a default, want none", got)
	}
	gitT(t, dir, "update-ref", "refs/remotes/origin/work", "HEAD")
	gitT(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/work")
	if got := c.Trunk(); got != "origin/work" {
		t.Errorf("Trunk = %q after origin/HEAD appeared, want origin/work: the miss was kept", got)
	}
}

func TestTrunk_ARemoteNameResolvesThroughItsHEADAsRevParseDoes(t *testing.T) {
	dir := repoWithCommit(t)
	gitT(t, dir, "update-ref", "refs/remotes/upstream/work", "HEAD")
	gitT(t, dir, "symbolic-ref", "refs/remotes/upstream/HEAD", "refs/remotes/upstream/work")
	gitT(t, dir, "config", "init.defaultBranch", "upstream")
	gitT(t, dir, "rev-parse", "--verify", "--quiet", "upstream")
	if got := mustNew(t, dir).Trunk(); got != "upstream" {
		t.Errorf("Trunk = %q, want upstream: git resolves it through refs/remotes/upstream/HEAD", got)
	}
}
