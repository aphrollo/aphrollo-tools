package tdd

import (
	"os/exec"
	"strings"
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
// only for a conflicted or --no-commit merge, so the receipt gate looked for a
// lane tip that does not exist yet and refused every clean merge. git names
// the branch it is merging in GIT_REFLOG_ACTION.
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
	_ = exec.Command("git", "-C", root, "merge", "other").Run() // conflicts: MERGE_HEAD lands
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

// A merge refused for a missing lane tip must say WHICH signal was missing,
// or the operator is left guessing at a gate that failed on its own inputs.
func TestMechanical_ReceiptRejectionNamesTheMissingMergeSignal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	res := Mechanical(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })
	if !res.Blocked {
		t.Fatal("a merge with no receipt must not land")
	}
	for _, want := range []string{"MERGE_HEAD", "GIT_REFLOG_ACTION"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not name %q", res.Message, want)
		}
	}
}
