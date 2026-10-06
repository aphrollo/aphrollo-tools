package mutation

import (
	"strings"
	"testing"
)

// canaryRebaseStopped puts the sibling lane in the middle of a rebase that
// stopped on a conflict, where git leaves its HEAD detached, and answers the
// snapshot taken before that. The commits that make the conflict are made
// first, so the only thing the run sees move is the rebase.
func canaryRebaseStopped(t *testing.T) (repo, sibling string, before gitWorld) {
	t.Helper()
	repo, sibling, _ = canaryLanes(t)
	write(t, sibling, "clash.txt", "sibling\n")
	gitDo(t, sibling, "add", "clash.txt")
	gitDo(t, sibling, "commit", "-qm", "sibling clash")
	write(t, repo, "clash.txt", "main\n")
	gitDo(t, repo, "add", "clash.txt")
	gitDo(t, repo, "commit", "-qm", "main clash")
	before = snapshotGitWorld(repo)
	gitOut(sibling, "rebase", "main") // stops on the conflict, exit status 1
	if head := strings.TrimSpace(gitOutT(t, sibling, "rev-parse", "--abbrev-ref", "HEAD")); head != "HEAD" {
		t.Fatalf("setup: the sibling's HEAD is %q, want it detached mid-rebase", head)
	}
	return repo, sibling, before
}

// Issue #1144: another lane rebasing while a proof ran detached that lane's
// HEAD, and the canary read it as the proof changing git state: an escape that
// was no leak.
func TestSnapshotGitWorld_ASiblingLaneStoppedMidRebaseIsNotAChange(t *testing.T) {
	repo, _, before := canaryRebaseStopped(t)

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("a sibling lane's rebase looked like a leak: %v", changes)
	}
}

// A detached HEAD with no rebase behind it is still a change: the exemption is
// the rebase, not the detaching.
func TestSnapshotGitWorld_ASiblingLaneDetachedOutsideARebaseIsStillAChange(t *testing.T) {
	repo, sibling, _ := canaryLanes(t)
	before := snapshotGitWorld(repo)

	gitDo(t, sibling, "checkout", "-q", "--detach")

	requireChange(t, before.changesTo(snapshotGitWorld(repo)), "the worktree HEADs")
}

// A lane that finishes its rebase during the run ends where it began.
func TestSnapshotGitWorld_ASiblingLaneThatFinishedItsRebaseIsNotAChange(t *testing.T) {
	repo, sibling, before := canaryRebaseStopped(t)
	gitDo(t, sibling, "rebase", "--abort")

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("an aborted rebase looked like a leak: %v", changes)
	}
}
