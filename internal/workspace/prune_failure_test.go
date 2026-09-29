package workspace

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubMergedAt makes branch read as a merged PR whose head is the worktree's
// own HEAD, so decide() selects it for removal.
func stubMergedAt(t *testing.T, wt, branch string) {
	t.Helper()
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	head := headSHA(t, wt)
	stubPRHeadOid(t, func(_, b string) (string, error) {
		if b == branch {
			return head, nil
		}
		return "", nil
	})
}

// A removal git refuses for a real reason is named — path and git's own text —
// on the receipt stream, one line per worktree, ahead of the tally.
func TestPrune_NamesTheWorktreeAndCauseOfARemovalFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root POSIX user to make the parent directory unwritable")
	}
	repo, wt, branch := preparedRepo(t)
	stubMergedAt(t, wt, branch)
	parent := filepath.Dir(wt)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	p, _ := PrunePlan(repo)
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "failed: "+wt+" (") || !strings.Contains(got, "Permission denied") {
		t.Errorf("receipt should carry `failed: <path> (<git's cause>)`:\n%s", got)
	}
	if !strings.Contains(got, "pruned 0, failed 1 worktree(s)") {
		t.Errorf("tally should still count the failure:\n%s", got)
	}
}

// A locked worktree is git leaving live work alone: reported as a skip with no
// failure count, and the tally stays the plain form.
func TestPrune_ALockedWorktreeIsASkipNotAFailure(t *testing.T) {
	repo, wt, branch := preparedRepo(t)
	stubMergedAt(t, wt, branch)
	if lock, err := exec.Command("git", "-C", repo, "worktree", "lock", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree lock: %v\n%s", err, lock)
	}

	p, _ := PrunePlan(repo)
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "skip: "+wt+" (locked)") {
		t.Errorf("a locked worktree should read as `skip: <path> (locked)`:\n%s", got)
	}
	if strings.Contains(got, "failed") {
		t.Errorf("a locked worktree is not a failure:\n%s", got)
	}
	if !strings.Contains(got, "pruned 0 worktree(s)") {
		t.Errorf("tally should be the plain form:\n%s", got)
	}
}

func TestExpectedRemoveSkip_OnlyGitDecliningHeldTrees(t *testing.T) {
	for _, tc := range []struct {
		msg    string
		reason string
		want   bool
	}{
		{"exit status 128: fatal: cannot remove a locked working tree, lock reason: x", "locked", true},
		{"exit status 128: fatal: '/w' contains modified or untracked files, use --force to delete it", "dirty", true},
		{"exit status 255: error: failed to delete '/w': Permission denied", "", false},
		{"unlink the links before removing the tree: boom", "", false},
	} {
		reason, ok := expectedRemoveSkip(errors.New(tc.msg))
		if ok != tc.want || reason != tc.reason {
			t.Errorf("expectedRemoveSkip(%q) = %q, %v; want %q, %v", tc.msg, reason, ok, tc.reason, tc.want)
		}
	}
}
