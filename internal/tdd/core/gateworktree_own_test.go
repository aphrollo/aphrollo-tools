package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// TestReadStagedTree_RefusesAnEmptyWorktree proves no worktree path never
// means "the directory this process stands in": read-tree -u --reset there
// would replace that checkout's files with another repo's index. The test
// stands in a scratch directory so that a regression cannot reach a real one.
func TestReadStagedTree_RefusesAnEmptyWorktree(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := readStagedTree(t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "no gate worktree") {
		t.Fatalf("readStagedTree(repo, \"\") = %v, want a refusal naming the missing worktree", err)
	}
}

// TestWriteGateOrigin_RecordsTheRepo proves a gate dir names the repo it
// belongs to, which is the only thing the gc sweep reads to keep it.
func TestWriteGateOrigin_RecordsTheRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt")
	writeGateOrigin(dir, "/repo")
	got, err := os.ReadFile(filepath.Join(dir, gcOriginFile))
	if err != nil || string(got) != "/repo" {
		t.Fatalf("origin = %q (%v), want /repo", got, err)
	}
}

// TestWriteGateOrigin_WritesNothingWithoutADirOrARepo proves an empty dir
// never means the process's own directory, and an empty repo is no origin.
func TestWriteGateOrigin_WritesNothingWithoutADirOrARepo(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	writeGateOrigin("", "/repo")
	dir := filepath.Join(t.TempDir(), "wt")
	writeGateOrigin(dir, "")
	if _, err := os.Stat(filepath.Join(cwd, gcOriginFile)); !os.IsNotExist(err) {
		t.Errorf("an empty dir wrote an origin into the working directory (%v)", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("an empty repo still made the gate dir (%v)", err)
	}
}

// TestGateWorktreeDir_IsStablePerRepoUnderTheStateDir proves the path is
// the same for one repo, different for another, and under the state dir.
func TestGateWorktreeDir_IsStablePerRepoUnderTheStateDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a, again, b := gateWorktreeDir("/repo/a"), gateWorktreeDir("/repo/a"), gateWorktreeDir("/repo/b")
	if a == "" || a != again || a == b || !strings.HasPrefix(a, filepath.Join(StateDir(), "failfirst-wt")) {
		t.Fatalf("gateWorktreeDir = %q, %q, %q; want one stable dir per repo under the state dir", a, again, b)
	}
}

// noStateDir leaves the process with no state dir at all.
func noStateDir(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if StateDir() != "" {
		t.Fatalf("setup: StateDir() = %q, want none", StateDir())
	}
}

func TestGateWorktreeDir_IsEmptyWithNoStateDir(t *testing.T) {
	noStateDir(t)
	if got := gateWorktreeDir("/repo"); got != "" {
		t.Fatalf("gateWorktreeDir = %q with no state dir, want \"\"", got)
	}
}

// gateRepo is a repo with one commit.
func gateRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tddtest.GitInit(t, root)
	tddtest.MustWrite(t, filepath.Join(root, "a.txt"), "a\n")
	tddtest.GitAddAll(t, root)
	tddtest.CommitAll(t, root)
	return root
}

// TestAddGateWorktree_ChecksHeadOutAtTheStablePath proves the checkout is
// HEAD, at the stable path, even over a leftover a crashed run left there,
// and that removeGateWorktree takes it away again.
func TestAddGateWorktree_ChecksHeadOutAtTheStablePath(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := gateRepo(t)
	stable := gateWorktreeDir(root)
	tddtest.MustWrite(t, filepath.Join(stable, "leftover.txt"), "stale\n")

	wt, err := addGateWorktree(root)
	if err != nil {
		t.Fatalf("addGateWorktree: %v", err)
	}
	if wt != stable {
		t.Errorf("worktree at %q, want the stable path %q", wt, stable)
	}
	// A checkout converts line endings by the box's core.autocrlf, so the
	// content is compared with CRLF folded: the point is that it is HEAD's.
	if got, err := os.ReadFile(filepath.Join(wt, "a.txt")); err != nil || strings.ReplaceAll(string(got), "\r\n", "\n") != "a\n" {
		t.Errorf("a.txt in the checkout = %q (%v), want HEAD's", got, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "leftover.txt")); !os.IsNotExist(err) {
		t.Errorf("the leftover survived the fresh checkout (%v)", err)
	}
	removeGateWorktree(root, wt)
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the checkout is still on disk after removal (%v)", err)
	}
}

// TestAddGateWorktree_FallsBackToATempDirWithNoStateDir proves a box with no
// state dir still gets a checkout, in a temp dir.
func TestAddGateWorktree_FallsBackToATempDirWithNoStateDir(t *testing.T) {
	root := gateRepo(t)
	noStateDir(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	wt, err := addGateWorktree(root)
	if err != nil {
		t.Fatalf("addGateWorktree: %v", err)
	}
	defer removeGateWorktree(root, wt)
	if !strings.HasPrefix(wt, tmp) {
		t.Errorf("worktree at %q, want it under the temp dir %q", wt, tmp)
	}
}

func TestAddGateWorktree_FailsWhenNoTempDirCanBeMade(t *testing.T) {
	root := gateRepo(t)
	noStateDir(t)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("TMP", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("TEMP", filepath.Join(t.TempDir(), "missing"))
	if wt, err := addGateWorktree(root); err == nil {
		removeGateWorktree(root, wt)
		t.Fatal("addGateWorktree made a checkout with no temp dir to put it in")
	}
}

// TestAddGateWorktree_FailsWithNoHeadAndLeavesNothing proves a repo with no
// commit gets an error, not an empty directory posing as a checkout.
func TestAddGateWorktree_FailsWithNoHeadAndLeavesNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	tddtest.GitInit(t, root)
	stable := gateWorktreeDir(root)
	wt, err := addGateWorktree(root)
	if err == nil {
		removeGateWorktree(root, wt)
		t.Fatal("addGateWorktree checked out a repo with no HEAD")
	}
	if _, serr := os.Stat(stable); !os.IsNotExist(serr) {
		t.Errorf("a failed checkout left %s behind (%v)", stable, serr)
	}
}

// deepStateDir puts the gate's state dir deep enough that the checkout under
// it, 41 characters further down, is past what git for Windows will make a
// worktree at: it made one at a path of 215 characters and refused one of 216
// with "fatal: '$GIT_DIR' too big". The checkout stays under 260, so only
// git's own rule is in play.
func deepStateDir(t *testing.T) {
	t.Helper()
	deep := t.TempDir()
	for len(deep) < 180 {
		deep = filepath.Join(deep, strings.Repeat("d", 30))
	}
	t.Setenv("CLAUDE_CONFIG_DIR", deep)
}

// TestAddGateWorktree_ADeepStateDirStillGetsACheckout proves the gate's
// checkout does not depend on how deep the state dir is: a box whose Claude
// config dir, or whose test run's temp dir, is long still gets the fail-first
// proof instead of a worktree git refuses to make.
func TestAddGateWorktree_ADeepStateDirStillGetsACheckout(t *testing.T) {
	deepStateDir(t)
	root := gateRepo(t)

	wt, err := addGateWorktree(root)
	if err != nil {
		t.Fatalf("addGateWorktree under a state dir of %d characters: %v", len(StateDir()), err)
	}
	defer removeGateWorktree(root, wt)
	if _, serr := os.Stat(filepath.Join(wt, "a.txt")); serr != nil {
		t.Errorf("the checkout at %q has no a.txt (%v)", wt, serr)
	}
}

// TestGateWorktreeDirWithin_PastTheLimitIsAShortStablePathTheCanaryKnows
// proves where the checkout goes when the state dir would put it past the
// limit: a path of the same stable-per-repo kind, under the temp dir, named
// the way the mutation canary recognises a gate checkout, and a path exactly
// at the limit does not move.
func TestGateWorktreeDirWithin_PastTheLimitIsAShortStablePathTheCanaryKnows(t *testing.T) {
	deepStateDir(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	long := gateWorktreeDirWithin("/repo/a", 0)

	if got := gateWorktreeDirWithin("/repo/a", len(long)); got != long {
		t.Errorf("a checkout path of exactly the limit moved to %q, want %q", got, long)
	}
	short := gateWorktreeDirWithin("/repo/a", len(long)-1)
	if !strings.HasPrefix(short, tmp) || !strings.HasPrefix(filepath.Base(short), "gate-failfirst-") || len(short) >= len(long) {
		t.Errorf("past the limit the checkout is at %q, want a shorter path named gate-failfirst-<id> under %q", short, tmp)
	}
	if again := gateWorktreeDirWithin("/repo/a", len(long)-1); again != short {
		t.Errorf("the short path is not stable: %q then %q", short, again)
	}
	if other := gateWorktreeDirWithin("/repo/b", len(long)-1); other == short {
		t.Errorf("two repos share the short path %q", short)
	}
}

// TestGitCheckoutPathLimit_OnlyWindowsHasOneAndItIsBelowWhatGitRefuses pins
// the limit's two sides: git for Windows made a worktree at 215 characters
// and refused 216, so a limit above 215 would still hand it a path it refuses;
// elsewhere there is no limit to apply.
func TestGitCheckoutPathLimit_OnlyWindowsHasOneAndItIsBelowWhatGitRefuses(t *testing.T) {
	got := gitCheckoutPathLimit()
	if runtime.GOOS != "windows" {
		if got != 0 {
			t.Errorf("gitCheckoutPathLimit() = %d on %s, want none", got, runtime.GOOS)
		}
		return
	}
	if got <= 0 || got > 215 {
		t.Errorf("gitCheckoutPathLimit() = %d on windows, want 1..215", got)
	}
}

// TestAddGateWorktree_AFailureCarriesGitsOwnWords proves the error says what
// git said, not only its exit status: a caller that has to tell an operator
// why the proof could not run has the cause in hand.
func TestAddGateWorktree_AFailureCarriesGitsOwnWords(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	tddtest.GitInit(t, root)

	wt, err := addGateWorktree(root)
	if err == nil {
		removeGateWorktree(root, wt)
		t.Fatal("addGateWorktree checked out a repo with no HEAD")
	}
	if !strings.Contains(err.Error(), "invalid reference: HEAD") {
		t.Errorf("error = %q, want git's own words (invalid reference: HEAD)", err)
	}
}
