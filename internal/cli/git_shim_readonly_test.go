package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestGitLockScope_ReadingSubverbsTakeNoLock is issue #575's remaining half.
// The queue exists to serialise WRITES; a verb that only reads must never
// sit behind one. `stash` was classified by its verb alone, so `git stash
// list` -- named in #575's own list of reads, alongside `git status` -- took
// the per-worktree index lock and waited out a merge gate that on a Rust
// workspace runs for minutes.
func TestGitLockScope_ReadingSubverbsTakeNoLock(t *testing.T) {
	unlocked := [][]string{
		{"stash", "list"},
		{"stash", "list", "--oneline"},
		{"stash", "show"},
		{"stash", "show", "-p", "stash@{1}"},
		// The rest of #575's list, pinned here so a future verb table
		// cannot quietly pull one of them into the queue.
		{"remote", "-v"},
		{"ls-files"},
		{"cat-file", "-p", "HEAD"},
		{"rev-parse", "HEAD"},
		{"show", "HEAD"},
	}
	for _, rest := range unlocked {
		if got := gitLockScopeFor(rest); got != gitNoLock {
			t.Errorf("git %s → scope %v, want no lock at all", strings.Join(rest, " "), got)
		}
	}

	// The other half of #575's requirement: the writes still queue. A stash
	// that writes the index or refs/stash is exactly what the lock is for.
	locked := [][]string{
		{"stash"},
		{"stash", "-u"},
		{"stash", "push", "-m", "wip"},
		{"stash", "save", "wip"},
		{"stash", "pop"},
		{"stash", "apply"},
		{"stash", "drop"},
		{"stash", "clear"},
		{"stash", "branch", "lane/x"},
	}
	for _, rest := range locked {
		if got := gitLockScopeFor(rest); got != gitWorktreeScope {
			t.Errorf("git %s → scope %v, want the per-worktree index scope", strings.Join(rest, " "), got)
		}
	}
}

// TestRunGitShim_StashListDoesNotQueueBehindAMerge is the same fix in
// behaviour, at the shim's own boundary: with the worktree lock held by a
// merge gate, `git stash list` must answer immediately rather than print a
// queued line and wait.
func TestRunGitShim_StashListDoesNotQueueBehindAMerge(t *testing.T) {
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
	cfg.waitBudget = 5 * time.Second
	cfg.pollInterval = 20 * time.Millisecond
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := runGitShim([]string{"stash", "list"}, strings.NewReader(""), &stdout, &stderr, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — a read must not queue behind a merge; stderr=%s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "queued") {
		t.Fatalf("stash list printed a queued line: %q", stderr.String())
	}
	if waited := time.Since(start); waited >= cfg.waitBudget {
		t.Fatalf("stash list waited %s, the whole budget — it must not wait at all", waited)
	}
}
