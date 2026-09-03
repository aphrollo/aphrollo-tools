package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// receiptRepo is an opted-in repo with a lane branch and a main that has moved
// on, so both directions of a merge can be described.
func receiptRepo(t *testing.T) (root string) {
	t.Helper()
	root = makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "opt in")
	gitDo(t, root, "branch", "-M", "main")

	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "src/lane.rs", "pub fn lane() -> i32 { 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")

	gitDo(t, root, "checkout", "-q", "main")
	write(t, root, "src/mainline.rs", "pub fn mainline() -> i32 { 2 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "main moves on")
	return root
}

// startMerge puts the repo in the state the pre-merge-commit hook sees: HEAD
// on the branch being merged INTO, MERGE_HEAD naming the branch coming in.
func startMerge(t *testing.T, root, into, from string) {
	t.Helper()
	gitDo(t, root, "checkout", "-q", into)
	sha, err := git(root, "rev-parse", from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "MERGE_HEAD"), []byte(strings.TrimSpace(sha)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(root, ".git", "MERGE_HEAD")) })
}

// The break: `git merge main` inside a lane worktree was refused for a missing
// receipt against MAIN's tip tree. A catch-up merge proves nothing about the
// lane — main is already reviewed and merged code — so the builder squash-
// merged instead and polluted the lane's merge-base diff with all of main's
// changes, which every later mutation run then had to measure.
func TestMutationReceiptStage_ACatchUpMergeOfMainIntoALaneNeedsNoReceipt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := receiptRepo(t)
	startMerge(t, root, "lane/x", "main")

	if got := mutationReceiptStage(root); got != nil && got.Blocked {
		t.Fatalf("merging main into a lane was refused: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "catchup-merge")
}

// The direction that MATTERS is unchanged: a lane going into main with no
// receipt is still refused, or the whole gate is decorative.
func TestMutationReceiptStage_ALaneMergingIntoMainStillNeedsAReceipt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := receiptRepo(t)
	startMerge(t, root, "main", "lane/x")

	got := mutationReceiptStage(root)
	if got == nil || !got.Blocked {
		t.Fatal("a lane merged into main with no mutation receipt")
	}
}

// A branch main already CONTAINS proves nothing either, whichever branch it is
// merged into: there is nothing in it that main has not already taken.
func TestMutationReceiptStage_ABranchMainAlreadyContainsIsACatchUp(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := receiptRepo(t)
	// A branch pointing at main's own parent: already contained by main.
	gitDo(t, root, "branch", "old/tip", "main~1")
	startMerge(t, root, "main", "old/tip")

	if got := mutationReceiptStage(root); got != nil && got.Blocked {
		t.Fatalf("merging an already-contained branch into main was refused: %s", got.Message)
	}
}
