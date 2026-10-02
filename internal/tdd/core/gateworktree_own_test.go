package core

import (
	"os"
	"path/filepath"
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
