package gc

import (
	"encoding/json"
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

	_, release, ok := TryAcquireBuildSlot(target, "cargo build", "/repo")
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

// TestApplyGC_TakesTheLockForAStrayTarget pins the consequence of issue
// #285's fix at the level that matters: `gate gc --apply` must not delete a
// stray-target candidate while something else holds a build slot for that
// EXACT path — a misresolved live target dir (the reported failure) or a
// genuinely stray one some other ad hoc `--target-dir` invocation is
// building into right now.
func TestApplyGC_TakesTheLockForAStrayTarget(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	repo := t.TempDir()
	stray := filepath.Join(repo, "target-sky")
	if err := os.MkdirAll(filepath.Join(stray, "debug"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, release, ok := TryAcquireBuildSlot(stray, "cargo build --target-dir target-sky", "/repo")
	if !ok {
		t.Fatal("could not occupy the stray target's slot")
	}
	defer release()

	freed, refused, skipped := ApplyGCFor(repo, []GCCandidate{{
		Path: stray, Reason: "stray cargo target dir", Kind: GCKindStrayTarget,
	}})
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("deleted a stray target dir a build holds (freed %d, refused %v, skipped %d)", freed, refused, skipped)
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

// writeLockOwnerPid records an arbitrary process as a lock's holder, which
// writeBuildLockOwnerAt cannot do — it always names the calling process.
func writeLockOwnerPid(t *testing.T, path string, pid int) {
	t.Helper()
	data, err := json.Marshal(BuildLockOwner{PID: pid, Cmd: "cargo build -p server", Cwd: "/repo", Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestApplyGCFor_OnlyAProvablyDeadHolderLosesItsProtection pins issue #565's
// second item: `gate gc --apply` reported "freed 0 B, a build holds every slot
// for this target dir" over a 7.5 GB target dir idle for three days whose
// recorded holder was not in the process list at all. A lock whose pid is dead
// is stale and protects nothing; a holder that is alive, or one that cannot be
// identified at all, keeps protecting the directory — deleting a target dir a
// live build is writing into is the expensive way to be wrong here.
func TestApplyGCFor_OnlyAProvablyDeadHolderLosesItsProtection(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	// Three target dirs are held at once here, and one global slot has to stay
	// free, or the sweep's own acquire would fail for a reason (a full box)
	// that has nothing to do with the lock under test.
	t.Setenv(buildSlotsEnv, "4")
	repo := t.TempDir()

	occupied := func(name string) string {
		t.Helper()
		dir := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Join(dir, "debug"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, release, ok := TryAcquireBuildSlot(dir, "cargo build -p server", repo)
		if !ok {
			t.Fatalf("setup: could not occupy the lock for %s", name)
		}
		t.Cleanup(release)
		return dir
	}
	sweep := func(dir string) (int64, int) {
		freed, refused, skipped := ApplyGCFor(repo, []GCCandidate{{
			Path: dir, Size: 7, Reason: "stray cargo target dir", Kind: GCKindStrayTarget,
		}})
		if len(refused) != 0 {
			t.Fatalf("refused %v", refused)
		}
		return freed, skipped
	}

	// A holder that is alive: the acquire above recorded THIS process, which
	// is as alive as a holder gets.
	live := occupied("target-live")
	if freed, skipped := sweep(live); skipped != 1 || freed != 0 {
		t.Errorf("live holder: freed=%d skipped=%d, want the dir left for next time", freed, skipped)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("deleted a target dir a live build holds: %v", err)
	}

	// A holder nobody can name: no owner record at all, which is what a lock
	// taken by something that never wrote one looks like.
	unknown := occupied("target-unknown")
	if err := os.Remove(ReadBuildSlotOwnerPath(unknown)); err != nil {
		t.Fatal(err)
	}
	if freed, skipped := sweep(unknown); skipped != 1 || freed != 0 {
		t.Errorf("unknown holder: freed=%d skipped=%d, want the dir left for next time", freed, skipped)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Errorf("deleted a target dir whose holder could not be identified: %v", err)
	}

	// A holder that is gone: a pid this large is no process on any OS this
	// runs on, asked of the real OS rather than a mock.
	dead := occupied("target-dead")
	writeLockOwnerPid(t, ReadBuildSlotOwnerPath(dead), 0x7FFFFFF0)
	if freed, skipped := sweep(dead); skipped != 0 || freed != 7 {
		t.Errorf("dead holder: freed=%d skipped=%d, want the 7 bytes reclaimed", freed, skipped)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("a lock whose holder is not on the box must not protect a target dir: %v", err)
	}
}
