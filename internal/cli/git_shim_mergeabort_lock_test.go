package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestRunGitShim_RejectedMergeAbortsWhileHoldingTheRepoLock pins the
// interlock the merge recovery depends on. `git merge --abort` rewrites the
// index AND the worktree, so it has to run under the same per-repo lock the
// merge itself ran under. Releasing first opens a window: another session
// polling for the lock takes it the moment the merge exits, starts its own
// `git commit`, and then loses its index to an abort firing underneath it
// (or is told "still mid-merge" about a checkout it has already moved on
// from).
//
// The hold is observed directly rather than by ordering bookkeeping:
// TryAcquireFileLock opens a FRESH file description, and both flock and
// LockFileEx are per-description, so it fails while the shim holds the lock
// even inside this one process. ok=true during the abort means nothing at
// all is protecting it.
func TestRunGitShim_RejectedMergeAbortsWhileHoldingTheRepoLock(t *testing.T) {
	gateConfigDir(t)
	withDirectGitShim(t)
	isolateGitConfigCLI(t)
	repo, branch := makeMergeableRepo(t)
	realGit := realGitForTest(t)

	root := tdd.RepoRoot(repo)
	if root == "" {
		t.Fatal("setup: could not resolve the repo root")
	}
	markerPath := tdd.MergeRejectedMarkerPath(root)
	if markerPath == "" {
		t.Fatal("setup: MergeRejectedMarkerPath returned empty")
	}
	installMarkerWritingHook(t, repo, markerPath)

	args := []string{"-C", repo, "merge", "--no-ff", branch}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	scope := gitLockScopeFor(args[2:])
	lockDir, ok := gitLockDir(realGit, args, cwd, scope)
	if !ok {
		t.Fatal("setup: could not resolve the lock dir for the fixture repo")
	}
	lockPath := filepath.Join(lockDir, gitLockFileFor(scope))

	var aborts, freeDuringAbort int
	prevAbort := mergeAbortFn
	mergeAbortFn = func(realGit, workDir string) (string, error) {
		aborts++
		if release, free := tdd.TryAcquireFileLock(lockPath); free {
			freeDuringAbort++
			release()
		}
		return prevAbort(realGit, workDir)
	}
	t.Cleanup(func() { mergeAbortFn = prevAbort })

	cfg := gitShimConfig{waitBudget: 5 * time.Second, pollInterval: 20 * time.Millisecond, realGit: realGit}
	var stdout, stderr bytes.Buffer
	code := runGitShim(args, strings.NewReader(""), &stdout, &stderr, cfg)
	if code == 0 {
		t.Fatalf("setup: expected the fake hook's rejection to propagate as a non-zero exit, got 0\nstderr: %s", stderr.String())
	}
	if aborts != 1 {
		t.Fatalf("setup: expected exactly one recovery abort, got %d\nstderr: %s", aborts, stderr.String())
	}
	if freeDuringAbort != 0 {
		t.Fatalf("`git merge --abort` ran with %s FREE: another session can take that lock the instant the merge exits and start a commit whose index this abort then rewrites underneath it", lockPath)
	}
	if !strings.Contains(stderr.String(), mergeRejectedRecoveryLine) {
		t.Fatalf("expected the recovery line %q, got stderr:\n%s", mergeRejectedRecoveryLine, stderr.String())
	}
	// The hold must end WITH the invocation: a lock kept past the return
	// wedges every later git in the repo.
	release, free := tdd.TryAcquireFileLock(lockPath)
	if !free {
		t.Fatalf("the shim must release %s before returning", lockPath)
	}
	release()
}

// TestRunGitWithLock_UnderLockWorkRunsBeforeTheOwnerRecordIsCleared pins the
// ordering runGitWithLock owes its underLock callback: it runs after the
// child git exits and BEFORE the owner record is removed and the lock
// released, so whatever it does to the index is as protected as the child's
// own work was. Its return value is the invocation's exit code.
func TestRunGitWithLock_UnderLockWorkRunsBeforeTheOwnerRecordIsCleared(t *testing.T) {
	ownerPath := filepath.Join(t.TempDir(), "aphrollo-git.owner")
	tdd.WriteFileLockOwner(ownerPath, "git merge", "somewhere")

	var order []string
	ownerSeenByUnderLock := false
	release := func() { order = append(order, "release") }
	underLock := func(code int) int {
		order = append(order, "underLock")
		if _, err := os.Stat(ownerPath); err == nil {
			ownerSeenByUnderLock = true
		}
		return code + 7
	}

	code := runGitWithLock(release, ownerPath, gitStub(t), []string{"merge", "lane"},
		strings.NewReader(""), os.Stdout, os.Stderr, underLock)

	if len(order) != 2 || order[0] != "underLock" || order[1] != "release" {
		t.Fatalf("order = %v, want the under-lock work to finish before the lock is released", order)
	}
	if !ownerSeenByUnderLock {
		t.Fatal("the owner record must still name this invocation while the under-lock work runs")
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7 (the stub exits 0 and the callback's own verdict is plumbed through)", code)
	}
}
