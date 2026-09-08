package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestGitLockScope_IndexVerbsAreWorktreeScoped pins which git dir a verb's
// lock belongs to. The index is PER WORKTREE (`index.lock` lives in that
// worktree's own git dir), so keying every mutation on the shared common dir
// made one lane's commit gate — which holds the lock for its whole run —
// block `git add` in every other worktree of the same repo. Verbs that
// genuinely mutate shared state (refs, the worktree registry, the object
// store) keep the common-dir key.
func TestGitLockScope_IndexVerbsAreWorktreeScoped(t *testing.T) {
	worktreeScoped := [][]string{
		{"add", "-A"},
		{"commit", "-m", "x"},
		{"checkout", "-b", "lane/x"},
		{"switch", "main"},
		{"reset", "--hard"},
		{"stash"},
		{"rm", "f"},
		{"mv", "a", "b"},
		{"rebase", "main"},
		{"cherry-pick", "abc"},
		{"revert", "abc"},
		{"am", "patch"},
		{"merge", "lane/x"},
		{"pull"},
		{"restore", "--staged", "f"},
		{"apply", "--index", "p.diff"},
	}
	for _, rest := range worktreeScoped {
		if got := gitLockScopeFor(rest); got != gitWorktreeScope {
			t.Errorf("git %s → scope %v, want the per-worktree index scope", strings.Join(rest, " "), got)
		}
	}

	repoScoped := [][]string{
		{"worktree", "add", "-b", "x", "d"},
		{"worktree", "remove", "d"},
		{"worktree", "prune"},
		{"branch", "-D", "lane/x"},
		{"branch", "-m", "old", "new"},
		{"fetch", "origin"},
		{"push", "origin", "HEAD"},
		{"gc", "--prune=now"},
	}
	for _, rest := range repoScoped {
		if got := gitLockScopeFor(rest); got != gitRepoScope {
			t.Errorf("git %s → scope %v, want the shared repo scope", strings.Join(rest, " "), got)
		}
	}

	unlocked := [][]string{
		{"status"},
		{"diff", "--cached"},
		{"log", "-1"},
		{"branch"},
		{"branch", "--list"},
		{"restore", "f"},
		{"apply", "p.diff"},
		{"worktree", "list"},
	}
	for _, rest := range unlocked {
		if got := gitLockScopeFor(rest); got != gitNoLock {
			t.Errorf("git %s → scope %v, want no lock at all", strings.Join(rest, " "), got)
		}
	}
}

// gitDirEnv points the stub's rev-parse at a per-worktree git dir (and its
// common dir), which is where the shim's lock and owner files then live.
func gitDirEnv(t *testing.T, gitDir, commonDir string) {
	t.Helper()
	t.Setenv("APHROLLO_TEST_GIT_DIR", gitDir)
	t.Setenv("APHROLLO_TEST_GIT_COMMON_DIR", commonDir)
}

// TestRunGitShim_TwoWorktreesOfOneRepoDoNotBlockEachOther is the fix in
// behaviour: a `git add` in lane A must not wait on lane B's commit gate.
// The lock lands in each worktree's OWN git dir.
func TestRunGitShim_TwoWorktreesOfOneRepoDoNotBlockEachOther(t *testing.T) {
	withDirectGitShim(t)
	common := t.TempDir()
	laneA := t.TempDir()
	gitDirEnv(t, laneA, common)

	// Lane B's gate is mid-commit: its lock file is held in ITS git dir.
	laneB := t.TempDir()
	release, ok := tdd.TryAcquireFileLock(filepath.Join(laneB, gitWorktreeLockFileName))
	if !ok {
		t.Fatal("setup: must be able to hold lane B's lock")
	}
	defer release()

	cfg := testGitShimConfig(t)
	cfg.waitBudget = 100 * time.Millisecond
	var stdout, stderr bytes.Buffer
	if code := runGitShim([]string{"add", "-A"}, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0 — another worktree's lock must not block this one; stderr=%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("an uncontended worktree must print nothing, got: %q", stderr.String())
	}
}

// TestRunGitShim_SameWorktreeStillQueues pins the half that must NOT change:
// two index mutations in the SAME worktree still serialise, which is the
// whole reason the lock exists.
func TestRunGitShim_SameWorktreeStillQueues(t *testing.T) {
	withDirectGitShim(t)
	common := t.TempDir()
	lane := t.TempDir()
	gitDirEnv(t, lane, common)

	release, ok := tdd.TryAcquireFileLock(filepath.Join(lane, gitWorktreeLockFileName))
	if !ok {
		t.Fatal("setup: must be able to hold this worktree's lock")
	}
	defer release()

	cfg := testGitShimConfig(t)
	cfg.waitBudget = 80 * time.Millisecond
	cfg.pollInterval = 10 * time.Millisecond
	var stdout, stderr bytes.Buffer
	if code := runGitShim([]string{"add", "-A"}, strings.NewReader(""), &stdout, &stderr, cfg); code != exGitTempFail {
		t.Fatalf("exit = %d, want %d — the same worktree must queue and give up", code, exGitTempFail)
	}
	if !strings.Contains(stderr.String(), "queued") {
		t.Fatalf("expected a queued line, got: %q", stderr.String())
	}
}

// TestRunGitShim_SharedStateVerbUsesTheCommonDir pins the other key: a
// `worktree remove` mutates the registry every worktree of the repo reads,
// so it must contend with the same verb run from a DIFFERENT worktree —
// which only happens if the lock lives in the common dir.
func TestRunGitShim_SharedStateVerbUsesTheCommonDir(t *testing.T) {
	withDirectGitShim(t)
	common := t.TempDir()
	gitDirEnv(t, t.TempDir(), common)

	release, ok := tdd.TryAcquireFileLock(filepath.Join(common, gitLockFileName))
	if !ok {
		t.Fatal("setup: must be able to hold the repo-wide lock")
	}
	defer release()

	cfg := testGitShimConfig(t)
	cfg.waitBudget = 80 * time.Millisecond
	cfg.pollInterval = 10 * time.Millisecond
	var stdout, stderr bytes.Buffer
	if code := runGitShim([]string{"worktree", "prune"}, strings.NewReader(""), &stdout, &stderr, cfg); code != exGitTempFail {
		t.Fatalf("exit = %d, want %d — a shared-state verb must contend on the common dir", code, exGitTempFail)
	}
}

// TestRunGitShim_PrimaryMergeDoesNotBlockRepoScopedVerbs pins the split: the
// primary checkout's own git dir IS the common dir, so its `git merge` —
// worktree-scoped, held for the whole pre-merge gate, hours when the merge
// measures mutants — must not sit on the file a `worktree add` from any lane
// waits for. Each scope has its own lock file.
func TestRunGitShim_PrimaryMergeDoesNotBlockRepoScopedVerbs(t *testing.T) {
	withDirectGitShim(t)
	common := t.TempDir()
	gitDirEnv(t, common, common)

	release, ok := tdd.TryAcquireFileLock(filepath.Join(common, gitWorktreeLockFileName))
	if !ok {
		t.Fatal("setup: must be able to hold the primary's worktree lock")
	}
	defer release()

	cfg := testGitShimConfig(t)
	cfg.waitBudget = 80 * time.Millisecond
	cfg.pollInterval = 10 * time.Millisecond
	var stdout, stderr bytes.Buffer
	if code := runGitShim([]string{"worktree", "prune"}, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
		t.Fatalf("exit = %d, want 0 — a repo-scoped verb must not queue behind the primary's merge: %s", code, stderr.String())
	}
}
