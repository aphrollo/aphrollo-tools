package mutation

import (
	"path/filepath"
	"strings"
	"testing"
)

// A lane is named after whatever its owner chose (feat/x, fix/y), not only
// lane/*, so a sibling session making one while a run is going is ordinary work
// (the fanpyp box recorded dozens of escapes for it).
func TestSnapshotGitWorld_AGitworldNewBranchOfASiblingLaneIsNotALeak(t *testing.T) {
	repo, _ := canaryRepo(t)
	before := snapshotGitWorld(repo)

	gitDo(t, repo, "worktree", "add", "-q", "-b", "feat/inbox", filepath.Join(laneDirOf(repo), "feat-inbox"))

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("a sibling's new feat/* lane looked like a leak: %v", changes)
	}
}

// A lane made and pruned, branch and all, between the two readings is gone at
// the end but was checked out at the start.
func TestSnapshotGitWorld_AGitworldLaneRemovedWithItsBranchMidRunIsNotALeak(t *testing.T) {
	repo, _ := canaryRepo(t)
	lane := filepath.Join(laneDirOf(repo), "fix-y")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "fix/y", lane)
	before := snapshotGitWorld(repo)

	gitDo(t, repo, "worktree", "remove", "--force", lane)
	gitDo(t, repo, "branch", "-D", "fix/y")

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("pruning a lane and its fix/* branch looked like a leak: %v", changes)
	}
}

// What a test leaks into the real repository stays caught: a branch no worktree
// has checked out, at a commit nobody made through the real commit path.
func TestSnapshotGitWorld_AGitworldBranchAtAFreshCommitCheckedOutNowhereIsALeak(t *testing.T) {
	repo, _ := canaryRepo(t)
	before := snapshotGitWorld(repo)

	tip := strings.TrimSpace(gitOutT(t, repo, "commit-tree", "-p", "HEAD", "-m", "fixture", "HEAD^{tree}"))
	gitDo(t, repo, "branch", "feat/leak", tip)

	requireChange(t, before.changesTo(snapshotGitWorld(repo)), "the branches")
}

// A leaked worktree on a new branch is caught through its registration even
// though the branch itself is checked out somewhere.
func TestSnapshotGitWorld_AGitworldLeakedWorktreeOnANewBranchIsStillALeak(t *testing.T) {
	repo, _ := canaryRepo(t)
	before := snapshotGitWorld(repo)

	gitDo(t, repo, "worktree", "add", "-q", "-b", "feat/leak", filepath.Join(t.TempDir(), "elsewhere"))

	requireChange(t, before.changesTo(snapshotGitWorld(repo)), "the worktree registrations")
}

// The commit gate checks out HEAD in the gate state's head-wt directory to
// baseline a dependency install, and removes it after.
func TestSnapshotGitWorld_AGitworldHeadBaselineCheckoutIsNotALeak(t *testing.T) {
	repo, _ := canaryRepo(t)
	path := filepath.Join(t.TempDir(), "gate-state", "head-wt", ".aphrollo-head-123456")
	before := snapshotGitWorld(repo)

	gitDo(t, repo, "worktree", "add", "-q", "--detach", path)
	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("the head baseline checkout looked like a leak: %v", changes)
	}
}

// The owner pulling or fast-forwarding to commits that are on origin moves the
// checked-out commit over commits no hook of this box recorded.
func TestSnapshotGitWorld_AGitworldFastForwardOverPublishedCommitsIsNotAChange(t *testing.T) {
	lane := recordedLane(t)
	repo := strings.TrimSpace(gitOutT(t, lane, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	repo = filepath.Dir(repo)
	before := snapshotGitWorld(lane)

	gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "someone else's, pushed")
	gitDo(t, repo, "update-ref", "refs/remotes/origin/main", "main")
	gitDo(t, lane, "merge", "-q", "--ff-only", "main")

	requireNoHeadChange(t, before.changesTo(snapshotGitWorld(lane)))
}

// The same move over a commit that is on no remote is a fixture commit leaked
// into the history the checkout stands on.
func TestSnapshotGitWorld_AGitworldFastForwardOverUnpublishedCommitsIsAChange(t *testing.T) {
	lane := recordedLane(t)
	repo := filepath.Dir(strings.TrimSpace(gitOutT(t, lane, "rev-parse", "--path-format=absolute", "--git-common-dir")))
	gitDo(t, repo, "update-ref", "refs/remotes/origin/main", "main")
	before := snapshotGitWorld(lane)

	gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "fixture")
	gitDo(t, lane, "merge", "-q", "--ff-only", "main")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), checkedOutLabel)
}
