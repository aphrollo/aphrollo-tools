package tdd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// noMutationRunLive tells every ownership probe the sweep asks that nothing
// is running, so a test is about the RULES rather than about whatever the box
// running it happens to be compiling.
func noMutationRunLive(t *testing.T) {
	t.Helper()
	running, copies, targets := mutantsRunningFn, mutantsCopyOwnerFn, targetDirOwnerFn
	mutantsRunningFn = func() bool { return false }
	mutantsCopyOwnerFn = func(string) (int, bool) { return 0, false }
	targetDirOwnerFn = func(string) (int, bool) { return 0, false }
	t.Cleanup(func() {
		mutantsRunningFn, mutantsCopyOwnerFn, targetDirOwnerFn = running, copies, targets
	})
}

// candidateAt indexes a scan by path, for a test that asserts about one row.
func candidateAt(cands []GCCandidate, path string) (GCCandidate, bool) {
	for _, c := range cands {
		if pathKey(c.Path) == pathKey(path) {
			return c, true
		}
	}
	return GCCandidate{}, false
}

// The sharded runner writes two kinds of directory beside a checkout, and gc
// called every one of them "a cargo-mutants tree copy" — the same reason, the
// same age bar and the same interlock for both. They are not the same thing:
//
//	shard-<i> is output and temp for one process. Once no run is live it is
//	   pure leftover, whatever its age.
//	target-<i> is a PERSISTENT build dir, kept on purpose so the next run is
//	   warm. Deleting one silently costs that run a cold build, so it is
//	   reclaimable only once stale, and the row has to say what it costs.
func TestGCMutantsRunDirs_ReclaimsAFinishedShardAndNamesAWarmTargetDirsCost(t *testing.T) {
	noMutationRunLive(t)
	area := filepath.Join(t.TempDir(), ".mutants", "lane-x")
	shard := filepath.Join(area, "shard-0")
	// Two hours old: well inside any age bar, and still garbage — the run
	// that owned it is over.
	mkFile(t, filepath.Join(shard, "mutants.out", "outcomes.json"), "[]", 2*time.Hour)
	stale := filepath.Join(area, "target-0")
	mkFile(t, filepath.Join(stale, cargoInfoFile), "{}", 9*24*time.Hour)
	mkFile(t, filepath.Join(stale, "debug", "libx.rlib"), "xxxxx", 9*24*time.Hour)
	warm := filepath.Join(area, "target-1")
	mkFile(t, filepath.Join(warm, cargoInfoFile), "{}", 0)

	got := gcMutantsRunDirs(area, 3*24*time.Hour, time.Now())

	if len(got) != 2 {
		t.Fatalf("candidates = %+v, want the finished shard and the stale build dir, and not the warm one", got)
	}
	if _, proposed := candidateAt(got, warm); proposed {
		t.Errorf("proposed %s — a build dir touched today is what makes the next run warm", warm)
	}
	c, ok := candidateAt(got, stale)
	if !ok {
		t.Fatalf("candidates = %+v, want the stale build dir %s", got, stale)
	}
	if c.Kind != GCKindMutantsTarget {
		t.Errorf("kind = %v, want its own kind: it is interlocked and explained differently from a leftover", c.Kind)
	}
	if !strings.Contains(c.Reason, "cold") {
		t.Errorf("reason = %q, want it to say what deleting it costs — the next run builds cold", c.Reason)
	}
	if s, ok := candidateAt(got, shard); !ok || s.Kind != GCKindMutants {
		t.Errorf("candidate for %s = %+v (found=%v), want the shard leftover proposed", shard, s, ok)
	}
}

// Liveness beats size, both ways round: a directory a run holds is left
// alone, and a probe that cannot answer keeps its protection (issue #565's
// rule, unchanged).
func TestGCMutantsRunDirs_LeavesEveryDirectoryALiveRunHolds(t *testing.T) {
	noMutationRunLive(t)
	area := filepath.Join(t.TempDir(), ".mutants", "lane-x")
	mkFile(t, filepath.Join(area, "shard-0", "mutants.out", "outcomes.json"), "[]", 2*time.Hour)
	mkFile(t, filepath.Join(area, "target-0", cargoInfoFile), "{}", 9*24*time.Hour)

	targetDirOwnerFn = func(string) (int, bool) { return 4242, true }
	for _, c := range gcMutantsRunDirs(area, 3*24*time.Hour, time.Now()) {
		if c.Kind == GCKindMutantsTarget {
			t.Errorf("proposed %s while a build owns it — deleting it walks a directory rustc is writing to", c.Path)
		}
	}

	targetDirOwnerFn = func(string) (int, bool) { return 0, false }
	mutantsCopyOwnerFn = func(string) (int, bool) { return 99, true }
	for _, c := range gcMutantsRunDirs(area, 3*24*time.Hour, time.Now()) {
		if c.Kind == GCKindMutants {
			t.Errorf("proposed %s while a run owns it — that is the tree it is mutating", c.Path)
		}
	}

	// A live run vetoes the area outright: a shard that finished early still
	// holds the outcomes file the merge reads when the last shard lands.
	mutantsCopyOwnerFn = func(string) (int, bool) { return 0, false }
	mutantsRunningFn = func() bool { return true }
	if got := gcMutantsRunDirs(area, 3*24*time.Hour, time.Now()); len(got) != 0 {
		t.Errorf("candidates = %+v, want nothing proposed while a mutation run is alive", got)
	}
}

// A persistent build dir is a cargo build directory, so it is deleted only
// while this process holds ITS OWN build slot — never under the repo's
// resolved target dir, which is a different lock entirely and guards nothing
// here.
func TestGCTargetInterlock_LocksAMutationBuildDirOnItsOwnPath(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	dir := filepath.Join(repo, "..", ".mutants", "lane-x", "target-2")

	if got := gcTargetInterlock(repo, GCCandidate{Path: dir, Kind: GCKindMutantsTarget}); got != dir {
		t.Errorf("interlock = %q, want the build dir itself %q", got, dir)
	}
}

// A lane's leftovers sit beside the LANE. A sweep run from the primary
// checkout that looked only at its own area reported 18 GB of stale artifacts
// while 350 GB of a lane's mutation run sat in a sibling directory nobody
// listed, and it was deleted by hand.
func TestScanGC_FindsALaneSiblingsMutationLeftovers(t *testing.T) {
	noMutationRunLive(t)
	repo := makeCargoRepo(t)
	parent := t.TempDir()
	lane := filepath.Join(parent, "lane-x")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "lane/x", lane)
	leftover := filepath.Join(measureTempDir(lane), "shard-0")
	mkFile(t, filepath.Join(leftover, "mutants.out", "outcomes.json"), "[]", 2*time.Hour)

	got := ScanGC(repo, 3*24*time.Hour, GCScope{Mutants: true})

	if _, found := candidateAt(got, leftover); !found {
		t.Fatalf("scan from the primary checkout missed the lane's own %s:\n%+v", leftover, got)
	}
}
