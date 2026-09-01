package tdd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestGC_StaleGateDirNeedsAMissingOrigin pins the difference between "the
// repo is gone" and "I could not look": any Stat error counted as deleted,
// so a permission error, a disconnected network drive or a path too long
// proposed a live gate worktree for deletion.
func TestGC_StaleGateDirNeedsAMissingOrigin(t *testing.T) {
	base := t.TempDir()
	live := filepath.Join(base, "failfirst-wt", "live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(live, gcOriginFile), []byte(repo), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gcStaleGateDirs(base); len(got) != 0 {
		t.Fatalf("proposed %v for a repo that exists", got)
	}

	unreadable := filepath.Join(base, "failfirst-wt", "unreadable")
	if err := os.MkdirAll(unreadable, 0o755); err != nil {
		t.Fatal(err)
	}
	// A path that cannot be a file name: Stat fails with something that is
	// NOT "does not exist", which must never read as "the repo is gone".
	if err := os.WriteFile(filepath.Join(unreadable, gcOriginFile), []byte(string([]byte{0})+"weird"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range gcStaleGateDirs(base) {
		if c.Path == unreadable {
			t.Fatal("proposed a gate dir whose origin could not be checked — only a proven-missing repo qualifies")
		}
	}
}

// TestGC_RegisteredWorktreeMatchIsCaseInsensitiveOnWindows pins a deletion:
// git prints a worktree path in one casing and the scan reads the parent
// directory in another (D:\ vs d:\ is routine on Windows), so a REGISTERED
// worktree failed the membership test and was proposed as an orphan.
func TestGC_RegisteredWorktreeMatchIsCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		if pathKey("/A/b") == pathKey("/a/b") {
			t.Fatal("path keys must stay case-SENSITIVE off Windows")
		}
		return
	}
	if pathKey(`D:\Projects\.worktrees\lane`) != pathKey(`d:\projects\.worktrees\LANE`) {
		t.Fatal("Windows path keys must case-fold, or a registered worktree reads as an orphan")
	}
}

// TestApplyGC_TakesTheLockForAnyCargoTarget pins the interlock: a RemoveAll
// over a directory that IS a cargo target (an orphan lane build dir, a gate
// target) races a build another session is running into it. Only the
// incremental category took the slot.
func TestApplyGC_TakesTheLockForAnyCargoTarget(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	repo := t.TempDir()
	lane := filepath.Join(repo, "lane")
	target := filepath.Join(lane, "target")
	if err := os.MkdirAll(filepath.Join(target, "debug"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, release, ok := TryAcquireBuildSlot(target)
	if !ok {
		t.Fatal("could not occupy the target's slot")
	}
	defer release()

	freed, refused, skipped := ApplyGCFor(repo, []GCCandidate{{
		Path: lane, Reason: "orphan build dir", Kind: GCKindOrphanWorktree,
	}})
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("deleted a target dir a build holds (freed %d, refused %v, skipped %d)", freed, refused, skipped)
	}
	if skipped == 0 {
		t.Fatal("a candidate left for next time must be reported as skipped")
	}
}

// TestGCStamp_OnlyOneSweeperWins pins the double sweep: two sessions
// starting together both saw the stamp as due and both spawned a sweep, so
// two RemoveAll walks ran over one tree.
func TestGCStamp_OnlyOneSweeperWins(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	first := claimGCSweep(time.Now())
	second := claimGCSweep(time.Now())
	if !first {
		t.Fatal("the first claim must win")
	}
	if second {
		t.Fatal("a second claim inside the interval must lose — the stamp is the claim")
	}
}

// TestSpawnBackgroundGC_IsDetached pins the half-done sweep: the stamp fires
// before the work, so a sweep killed with the hook's process group leaves
// the tree half-deleted and no sweep due for another day.
func TestSpawnBackgroundGC_IsDetached(t *testing.T) {
	if !strings.Contains(backgroundGCSpawnDescription(), "detached") {
		t.Fatal("the background sweep must outlive the hook that starts it")
	}
}
