package mutation

import "testing"

// The checked-out commit of the lane a runner watches moves only through a
// commit, and no record exists for every commit its owner makes: the gate note
// is written only for a proven suite, so a docs-only or unproven commit has
// none. A leaked fixture commit under the box's own identity is also a
// descendant authored and committed by the owner's email. Neither can be told
// from the owner's work, so any move of the checked-out commit counts (#1074,
// #1076 stay open until a record written for every gated commit exists).

// ratchet: test_removed TestSnapshotGitWorld_TheOwnersCommitsOnTopOfTheCheckedOutCommitAreNotAChange: the identity rule it pinned was dropped; any HEAD move counts again
// ratchet: test_removed TestSnapshotGitWorld_ACommitUnderAnotherIdentityIsAChange: replaced by the any-commit rule below
// ratchet: test_removed TestSnapshotGitWorld_OneForeignCommitAmongTheOwnersIsAChange: replaced by the any-commit rule below
// ratchet: test_removed TestSnapshotGitWorld_AMoveBackToAnEarlierCommitIsAChange: covered by the any-commit rule below

func TestSnapshotGitWorld_ACommitUnderTheOwnersOwnIdentityIsStillAChange(t *testing.T) {
	repo, _, _ := canaryLanes(t)
	lane := t.TempDir() + "/mine"
	gitDo(t, repo, "worktree", "add", "-q", "-b", "mine", lane)
	before := snapshotGitWorld(lane)

	// The repository's own configured identity, as a leak that inherited the
	// box's global one would commit under.
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "leak")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), "the checked-out commit")
}

func TestSnapshotGitWorld_AResetOfTheCheckedOutCommitIsStillAChange(t *testing.T) {
	repo, _, _ := canaryLanes(t)
	lane := t.TempDir() + "/mine"
	gitDo(t, repo, "worktree", "add", "-q", "-b", "mine", lane)
	gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "mine")
	before := snapshotGitWorld(lane)

	gitDo(t, lane, "reset", "-q", "--hard", "HEAD~1")

	requireChange(t, before.changesTo(snapshotGitWorld(lane)), "the checked-out commit")
}
