package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGateDirs_RecordTheirOriginAtCreation pins the fact the whole stale-dir
// sweep depends on: both gate directories are named by a hash, so unless the
// repo they belong to is written down beside them at creation, nothing can
// ever tell a live one from the remains of a deleted repo.
func TestGateDirs_RecordTheirOriginAtCreation(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()

	target := cargoFailFirstTarget(repo)
	if target == "" {
		t.Fatal("setup: expected a gate target dir")
	}
	wt := failFirstWorktreeDir(repo)
	if wt == "" {
		t.Fatal("setup: expected a fail-first worktree dir")
	}
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	// The worktree dir's origin is written when the gate creates the dir,
	// which is what failFirstWorktreeDir is called for.
	failFirstWorktreeDir(repo)

	for _, dir := range []string{target, wt} {
		got, err := os.ReadFile(filepath.Join(dir, gcOriginFile))
		if err != nil {
			t.Fatalf("%s has no %s: %v", dir, gcOriginFile, err)
		}
		if !strings.Contains(string(got), filepath.Base(repo)) {
			t.Errorf("%s records %q, want the repo it belongs to (%s)", dir, got, repo)
		}
	}

	// Once the repo is gone, both become reclaimable — the end-to-end point
	// of recording the origin.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, c := range gcStaleGateDirs(stateDir()) {
		found[c.Path] = true
	}
	if !found[target] || !found[wt] {
		t.Fatalf("both gate dirs must be reclaimable once their repo is gone, found: %v", found)
	}
}

// TestGCDue_AtMostOncePerDay pins the background sweep's rate limit: it runs
// at most once per 24h per box, so opening five sessions in a morning does
// not walk hundreds of gigabytes five times.
func TestGCDue_AtMostOncePerDay(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	now := time.Now()

	if !gcDue(now) {
		t.Fatal("with no stamp at all the sweep must be due")
	}
	stampGC(now)
	if gcDue(now.Add(3 * time.Hour)) {
		t.Fatal("three hours after a sweep it must NOT be due again")
	}
	if !gcDue(now.Add(25 * time.Hour)) {
		t.Fatal("a day later it must be due again")
	}
}

// TestGCReportLine_ReportsOnceAndOnlyWhenSomethingWasFreed pins the session
// line: the background sweep finished after the session that started it
// exited, so the NEXT session is what tells the operator — exactly once, and
// never when the sweep freed nothing.
func TestGCReportLine_ReportsOnceAndOnlyWhenSomethingWasFreed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if line := gcReportLine(); line != "" {
		t.Fatalf("with no report at all the session must be silent, got %q", line)
	}

	writeGCReport(12_884_901_888, 7)
	line := gcReportLine()
	if !strings.Contains(line, "12.0 GB") || !strings.Contains(line, "7") {
		t.Fatalf("report line = %q, want the amount freed and the directory count", line)
	}
	if second := gcReportLine(); second != "" {
		t.Fatalf("the same sweep must be reported ONCE, got a second line: %q", second)
	}

	writeGCReport(0, 0)
	if line := gcReportLine(); line != "" {
		t.Fatalf("a sweep that freed nothing must be silent, got %q", line)
	}
}

// TestHandleSessionStart_StartsTheSweepAndCarriesTheLastReport pins the
// wiring: session start kicks off the background sweep when it is due (never
// running it inline — the hook must not block on hundreds of gigabytes) and
// carries the previous sweep's result as ONE line alongside the nudge.
func TestHandleSessionStart_StartsTheSweepAndCarriesTheLastReport(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []string
	gcSpawnForTest = func(cwd string) { started = append(started, cwd) }
	t.Cleanup(func() { gcSpawnForTest = nil })

	writeGCReport(1_073_741_824, 2)
	raw := []byte(`{"session_id":"s1","cwd":"D:/Projects/borld"}`)

	got := HandleSessionStart(raw)
	if !strings.Contains(got, "1.0 GB") {
		t.Errorf("session start must carry the last sweep's result, got: %s", got)
	}
	if !strings.Contains(got, skillNudge) {
		t.Error("the gc line must be added to the nudge, not replace it")
	}
	if len(started) != 1 || started[0] != "D:/Projects/borld" {
		t.Fatalf("expected one background sweep started in the session's cwd, got %v", started)
	}

	// Second session the same day: stamped, so no second sweep.
	HandleSessionStart(raw)
	if len(started) != 1 {
		t.Fatalf("the sweep must run at most once per day, got %d starts", len(started))
	}
}

// TestApplyGCFor_IncrementalNeedsASlotOthersDoNot pins the interlock: an
// incremental cache can be deleted out from under a RUNNING build, so that
// category is swept only while holding a build slot for the target dir and
// is skipped silently otherwise (the next session gets it). Stale gate dirs
// and orphan build dirs belong to nothing that is running, so they never
// wait for a slot.
func TestApplyGCFor_IncrementalNeedsASlotOthersDoNot(t *testing.T) {
	withIsolatedBuildLock(t)
	repo := t.TempDir()

	inc := filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkFile(t, filepath.Join(inc, "f"), "12345", 0)
	orphan := filepath.Join(t.TempDir(), "lane-gone")
	mkFile(t, filepath.Join(orphan, "target", "x"), "123", 0)
	cands := []GCCandidate{
		{Path: inc, Size: 5, Reason: "incremental cache, idle 9d", Kind: GCKindIncremental},
		{Path: orphan, Size: 3, Reason: "orphan build dir", Kind: GCKindOrphanWorktree},
	}

	_, release, ok := TryAcquireBuildSlot(ResolveCargoTargetDir(repo))
	if !ok {
		t.Fatal("setup: must be able to saturate the repo's only slot")
	}

	freed, _, skipped := ApplyGCFor(repo, cands)
	if skipped != 1 {
		t.Errorf("skipped = %d, want the one incremental cache deferred while a build holds the slot", skipped)
	}
	if _, err := os.Stat(inc); err != nil {
		t.Error("an incremental cache must survive a sweep that could not get a slot")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("an orphan build dir must be swept regardless of the slots")
	}
	if freed != 3 {
		t.Errorf("freed = %d, want only the orphan's 3 bytes", freed)
	}

	release()
	freed, _, skipped = ApplyGCFor(repo, cands[:1])
	if skipped != 0 || freed != 5 {
		t.Fatalf("with the slot free the incremental cache must go: freed=%d skipped=%d", freed, skipped)
	}
	if _, err := os.Stat(inc); !os.IsNotExist(err) {
		t.Error("the incremental cache must be gone once a slot was available")
	}
}

// TestGCAfterWorktreeChange_SweepsWhatTheRemovalLeftBehind pins the
// immediate deletions the git shim triggers: `git worktree remove` drops the
// checkout but leaves the (often tens of GB) target/ behind, and the gate's
// own dirs for that root become dead the moment it goes.
func TestGCAfterWorktreeChange_SweepsWhatTheRemovalLeftBehind(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeCargoRepo(t)
	wtParent := t.TempDir()
	lane := filepath.Join(wtParent, "lane-x")

	gitDo(t, repo, "worktree", "add", "-b", "lane/x", lane)
	// The lane had its own gate dirs and its own build dir. The fail-first
	// worktree is keyed on the LANE, so it dies with it; the warm cargo
	// target is keyed on the repo they all share, so it must survive.
	gateWorktree := failFirstWorktreeDir(lane)
	mkFile(t, filepath.Join(gateWorktree, "src", "lib.rs"), "x", 0)
	sharedTarget := cargoFailFirstTarget(lane)
	mkFile(t, filepath.Join(lane, "target", "debug", "big.rlib"), "0123456789", 0)
	gitDo(t, repo, "worktree", "remove", "--force", lane)
	// git leaves a build dir it never created.
	mkFile(t, filepath.Join(lane, "target", "debug", "big.rlib"), "0123456789", 0)

	freed := GCAfterWorktreeChange(repo, lane)

	if _, err := os.Stat(lane); !os.IsNotExist(err) {
		t.Error("the removed worktree's leftover build dir must be swept immediately")
	}
	if _, err := os.Stat(gateWorktree); !os.IsNotExist(err) {
		t.Error("the gate's fail-first worktree for a removed worktree must be swept immediately")
	}
	if _, err := os.Stat(sharedTarget); err != nil {
		t.Error("the warm target is keyed on the repo every worktree shares — removing one lane must never take it")
	}
	if freed <= 0 {
		t.Errorf("freed = %d, want the bytes actually reclaimed", freed)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatal("the repo itself must never be touched")
	}
}
