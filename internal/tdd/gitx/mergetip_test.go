package gitx

import (
	"os/exec"
	"testing"
)

// laneRepo commits a base, puts one commit on `lane`, and returns the repo
// with `lane` NOT checked out — the state a merge starts from.
func laneRepo(t *testing.T) (root, base, laneTree string) {
	t.Helper()
	root = makeGoRepo(t)
	base = gitValue(t, root, "rev-parse", "--abbrev-ref", "HEAD")
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "lane.go", "package m\n\nconst Lane = 1\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	laneTree = gitValue(t, root, "rev-parse", "lane:")
	gitDo(t, root, "checkout", "-q", base)
	return root, base, laneTree
}

// An AUTOMERGE — the clean `git merge --no-ff lane` every lane lands with —
// fires pre-merge-commit BEFORE .git/MERGE_HEAD is written. MERGE_HEAD exists
// only for a conflicted or --no-commit merge, so a stage that looks for the
// lane tip there alone finds one that does not exist yet and refuses every
// clean merge. git names the branch it is merging in GIT_REFLOG_ACTION.
func TestMergeTipTree_ResolvesTheLaneTipFromTheReflogAction(t *testing.T) {
	root, _, laneTree := laneRepo(t)
	t.Setenv("GIT_REFLOG_ACTION", "merge lane")

	if got := mergeTipTree(root); got != laneTree {
		t.Fatalf("mergeTipTree = %q, want the lane tip's tree %q", got, laneTree)
	}
}

// MERGE_HEAD is the stronger signal: when it exists it IS the merge in
// progress, and a stale reflog action must not outrank it.
func TestMergeTipTree_PrefersMergeHeadOverTheReflogAction(t *testing.T) {
	root, base, _ := laneRepo(t)
	gitDo(t, root, "checkout", "-q", "-b", "other", base)
	write(t, root, "lane.go", "package m\n\nconst Lane = 2\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "other")
	otherTree := gitValue(t, root, "rev-parse", "other:")

	gitDo(t, root, "checkout", "-q", base)
	write(t, root, "lane.go", "package m\n\nconst Lane = 3\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base-side")
	_ = exec.Command(gitBinary(), "-C", root, "merge", "other").Run() // conflicts: MERGE_HEAD lands
	t.Setenv("GIT_REFLOG_ACTION", "merge lane")

	if got := mergeTipTree(root); got != otherTree {
		t.Fatalf("mergeTipTree = %q, want MERGE_HEAD's tree %q", got, otherTree)
	}
}

func TestMergeTipTree_EmptyWhenNothingNamesAMerge(t *testing.T) {
	root, _, _ := laneRepo(t)
	t.Setenv("GIT_REFLOG_ACTION", "commit: something else")

	if got := mergeTipTree(root); got != "" {
		t.Fatalf("mergeTipTree = %q, want empty — no merge is in progress", got)
	}
}

// ratchet: test_removed TestMechanical_ReceiptRejectionNamesTheMissingMergeSignal: the receipt stage that refused on a missing lane tip is deleted; mergeTipTree still resolves the tip, proved by the three tests above
