package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeGoRepoAt is makeGoRepo with a caller-chosen directory (and therefore a
// caller-chosen basename) instead of a random t.TempDir() name — needed to
// build two repos that deliberately DO or DO NOT share a folder name.
func makeGoRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	isolateGitConfig(t)
	return copyFixture(t, dir, goFixture)
}

// TestCheckMutationReceipt_MainCheckoutAccepts pins the base case: a
// receipt whose Repo names the checkout's OWN git common dir is accepted by
// that same checkout.
func TestCheckMutationReceipt_MainCheckoutAccepts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	dir := commonGitDir(root)
	if dir == "" {
		t.Fatal("setup: could not resolve the repo's common git dir")
	}

	r := passingReceipt()
	r.Repo = dir
	r.TipTree = laneTip
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: dir, TipTree: laneTip}); got != nil {
		t.Fatalf("the main checkout must accept its own receipt: %s", got.Message)
	}
}

// TestCheckMutationReceipt_LinkedWorktreeDifferentName_Accepts pins the
// defect fix: a merge running in a LINKED worktree whose directory name
// differs from the repo's must still accept a receipt the producer wrote
// from wherever IT ran (the main checkout, or another worktree) -- every
// worktree of one repo shares the SAME git common dir regardless of the
// worktree's own folder name, and that shared dir is what identifies "one
// repo", not any one worktree's directory name.
func TestCheckMutationReceipt_LinkedWorktreeDifferentName_Accepts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	mainDir := commonGitDir(root)
	if mainDir == "" {
		t.Fatal("setup: could not resolve the main checkout's common git dir")
	}

	// Deliberately named unlike the repo -- the exact shape of the reported
	// defect (a worktree at .../borld/eol merging against a receipt written
	// for D:/Projects/borld/.git).
	wtDir := filepath.Join(t.TempDir(), "eol")
	gitDo(t, root, "worktree", "add", "-q", "--detach", wtDir)
	t.Cleanup(func() { gitDo(t, root, "worktree", "remove", "--force", wtDir) })

	wtDirCommon := commonGitDir(wtDir)
	if wtDirCommon == "" {
		t.Fatal("setup: could not resolve the linked worktree's common git dir")
	}
	if !strings.EqualFold(wtDirCommon, mainDir) {
		t.Fatalf("setup: expected the worktree to share the main checkout's common dir, got %q vs %q", wtDirCommon, mainDir)
	}

	r := passingReceipt()
	r.Repo = mainDir // the producer resolved this from wherever it ran
	r.TipTree = laneTip
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: wtDirCommon, TipTree: laneTip}); got != nil {
		t.Fatalf("a linked worktree with a different folder name must accept the main checkout's receipt: %s", got.Message)
	}
}

// TestCheckMutationReceipt_DifferentRepoSameFolderName_Refuses pins the
// half a basename-only comparison gets WRONG: two unrelated repos that
// happen to share a folder name ("borld" under two different parents) must
// never be treated as the same repo just because their last path segment
// matches -- comparing the full git common dir is what tells them apart.
func TestCheckMutationReceipt_DifferentRepoSameFolderName_Refuses(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	rootA := makeGoRepoAt(t, filepath.Join(t.TempDir(), "borld"))
	rootB := makeGoRepoAt(t, filepath.Join(t.TempDir(), "borld"))
	dirA := commonGitDir(rootA)
	dirB := commonGitDir(rootB)
	if dirA == "" || dirB == "" {
		t.Fatal("setup: could not resolve both repos' common git dirs")
	}
	if strings.EqualFold(dirA, dirB) {
		t.Fatalf("setup: expected two independently-created repos to have DIFFERENT common dirs despite sharing a folder name, got %q == %q", dirA, dirB)
	}

	r := passingReceipt()
	r.Repo = dirA
	r.TipTree = laneTip
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: dirB, TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt for a DIFFERENT repo that merely shares a folder name must be refused")
	}
}

// TestMechanical_LinkedWorktreeDifferentName_AcceptsMainCheckoutReceipt is
// the literal reported defect, reproduced through the REAL call path
// (Mechanical -> mutationReceiptStage), not just checkMutationReceipt called
// directly: a merge running in a linked worktree named unlike the repo
// (`.worktrees/borld/eol`) used to be refused a receipt the main checkout's
// producer wrote, because the gate compared the WORKTREE's own folder name
// against the producer's git-dir path instead of the shared git common dir.
func TestMechanical_LinkedWorktreeDifferentName_AcceptsMainCheckoutReceipt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, base, laneTree := laneRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "enable mutation-receipt")

	mainDir := commonGitDir(root)
	if mainDir == "" {
		t.Fatal("setup: could not resolve the main checkout's common git dir")
	}

	// --detach: `base` is already checked out in the main checkout, and git
	// refuses to check the same branch out in two worktrees at once. Detached
	// at base's own tip still carries the mutation-receipt commit above, so
	// the worktree's own Cargo.toml has the flag too.
	wtDir := filepath.Join(t.TempDir(), "eol")
	gitDo(t, root, "worktree", "add", "-q", "--detach", wtDir, base)
	t.Cleanup(func() { gitDo(t, root, "worktree", "remove", "--force", wtDir) })

	r := passingReceipt()
	r.Repo = mainDir // written by the producer running in the MAIN checkout
	r.TipTree = laneTree
	writeReceipt(t, r)

	t.Setenv("GIT_REFLOG_ACTION", "merge lane")
	res := Mechanical(wtDir, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })
	if res.Blocked {
		t.Fatalf("a linked worktree named unlike the repo must accept the main checkout's receipt: %s", res.Message)
	}
}
