package mutation

import (
	"testing"
)

// A runner's canary window can be minutes long, and the lane it watches is
// the one its owner is committing in (#1074, #1076). Commits the owner makes
// on top of the checked-out commit are ordinary work; a commit made under any
// other identity, a move that is no descent, or a mix, still is a change.

func headLane(t *testing.T) string {
	t.Helper()
	repo, _, _ := canaryLanes(t)
	lane := t.TempDir() + "/mine"
	gitDo(t, repo, "worktree", "add", "-q", "-b", "mine", lane)
	return lane
}

func TestSnapshotGitWorld_TheOwnersCommitsOnTopOfTheCheckedOutCommitAreNotAChange(t *testing.T) {
	lane := headLane(t)
	before := snapshotGitWorld(lane)

	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "first")
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "second")

	if changes := before.changesTo(snapshotGitWorld(lane)); len(changes) != 0 {
		t.Errorf("the owner's own commits read as a leak: %v", changes)
	}
}

func TestSnapshotGitWorld_ACommitUnderAnotherIdentityIsAChange(t *testing.T) {
	lane := headLane(t)
	before := snapshotGitWorld(lane)

	gitDo(t, lane, "-c", "user.email=fixture@example.com", "-c", "user.name=fixture", "commit", "-q", "--allow-empty", "-m", "fixture")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), "the checked-out commit")
}

func TestSnapshotGitWorld_OneForeignCommitAmongTheOwnersIsAChange(t *testing.T) {
	lane := headLane(t)
	before := snapshotGitWorld(lane)

	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "mine")
	gitDo(t, lane, "-c", "user.email=fixture@example.com", "-c", "user.name=fixture", "commit", "-q", "--allow-empty", "-m", "fixture")
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "mine again")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), "the checked-out commit")
}

func TestSnapshotGitWorld_AMoveBackToAnEarlierCommitIsAChange(t *testing.T) {
	lane := headLane(t)
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "mine")
	before := snapshotGitWorld(lane)

	gitDo(t, lane, "reset", "-q", "--hard", "HEAD~1")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), "the checked-out commit")
}
