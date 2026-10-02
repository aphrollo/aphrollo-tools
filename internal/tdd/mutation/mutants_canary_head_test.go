package mutation

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/commitrecord"
)

// ownerCommit makes a commit in lane the way the owner does, through the
// post-commit hook: the commit, then the record the hook writes.
func ownerCommit(t *testing.T, lane, message string) {
	t.Helper()
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", message)
	commitrecord.Record(lane)
}

func recordedLane(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo, _, _ := canaryLanes(t)
	lane := t.TempDir() + "/mine"
	gitDo(t, repo, "worktree", "add", "-q", "-b", "mine", lane)
	return lane
}

func requireNoHeadChange(t *testing.T, changes []string) {
	t.Helper()
	for _, c := range changes {
		if strings.HasPrefix(c, checkedOutLabel) {
			t.Fatalf("the owner's commits were counted: %v", changes)
		}
	}
}

func TestSnapshotGitWorld_CommitsThroughTheHookAreNotAChange(t *testing.T) {
	lane := recordedLane(t)
	before := snapshotGitWorld(lane)

	ownerCommit(t, lane, "one")
	ownerCommit(t, lane, "two")

	requireNoHeadChange(t, before.changesTo(snapshotGitWorld(lane)))
}

func TestSnapshotGitWorld_AnAmendThroughTheHookIsNotAChange(t *testing.T) {
	lane := recordedLane(t)
	ownerCommit(t, lane, "one")
	before := snapshotGitWorld(lane)

	ownerCommit(t, lane, "two")
	gitDo(t, lane, "commit", "-q", "--amend", "--allow-empty", "-m", "two, amended")
	commitrecord.Record(lane)

	requireNoHeadChange(t, before.changesTo(snapshotGitWorld(lane)))
}

func TestSnapshotGitWorld_OneUnrecordedCommitAmongTheOwnersIsAChange(t *testing.T) {
	lane := recordedLane(t)
	before := snapshotGitWorld(lane)

	ownerCommit(t, lane, "mine")
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "leak, hooks disabled")
	ownerCommit(t, lane, "mine again")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), checkedOutLabel)
}

// The checked-out commit of the lane a runner watches moves through a commit,
// and the lane's owner commits while a minutes-long run is going (#1074, #1076).
// A leaked fixture commit under the box's own identity looks the same, so the
// canary tells them apart by the record the post-commit hook writes for every
// commit made through the real commit path: a test process runs with the hooks
// neutralised and leaves none.

// ratchet: test_removed TestSnapshotGitWorld_ACommitUnderTheOwnersOwnIdentityIsStillAChange: renamed to ...WithoutARecordIsAChange; a record now separates owner commits from a leak
// ratchet: test_removed TestSnapshotGitWorld_AResetOfTheCheckedOutCommitIsStillAChange: renamed to ...EvenWhenRecorded
// ratchet: test_removed TestSnapshotGitWorld_TheOwnersCommitsOnTopOfTheCheckedOutCommitAreNotAChange: the identity rule it pinned was dropped; any HEAD move counts again
// ratchet: test_removed TestSnapshotGitWorld_ACommitUnderAnotherIdentityIsAChange: replaced by the any-commit rule below
// ratchet: test_removed TestSnapshotGitWorld_OneForeignCommitAmongTheOwnersIsAChange: replaced by the any-commit rule below
// ratchet: test_removed TestSnapshotGitWorld_AMoveBackToAnEarlierCommitIsAChange: covered by the any-commit rule below

func TestSnapshotGitWorld_ACommitUnderTheOwnersIdentityWithoutARecordIsAChange(t *testing.T) {
	lane := recordedLane(t)
	before := snapshotGitWorld(lane)

	// The repository's own configured identity, as a leak that inherited the
	// box's global one would commit under.
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "leak")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), "the checked-out commit")
}

func TestSnapshotGitWorld_AResetOfTheCheckedOutCommitIsAChangeEvenWhenRecorded(t *testing.T) {
	lane := recordedLane(t)
	ownerCommit(t, lane, "mine")
	before := snapshotGitWorld(lane)

	gitDo(t, lane, "reset", "-q", "--hard", "HEAD~1")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), "the checked-out commit")
}
