package suite

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A cargo artifact stamped ahead of "now" is fresh forever: cargo compares
// source mtime against artifact mtime, so however the source changes the
// package is never rebuilt. Issue #276's merge rejected a correct tree this
// way. runCargoLocked must wipe a target dir carrying one before running the
// command — every cargo stage goes through this one choke point, precommit
// and the merge gate alike.
func TestRunCargoLocked_WipesATargetDirCarryingAFutureStampedArtifact(t *testing.T) {
	withIsolatedBuildLock(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	stale := filepath.Join(target, "debug", "deps", "libmovement.rmeta")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(4 * time.Hour)
	if err := os.Chtimes(stale, future, future); err != nil {
		t.Fatal(err)
	}
	// A healthy, ordinary file alongside it — the wipe must not spare it: a
	// future-stamped file anywhere in target makes cargo's freshness
	// comparisons untrustworthy for every package sharing that target dir,
	// not just the one file that carries the bad stamp.
	healthy := filepath.Join(target, "debug", "deps", "libshared.rmeta")
	if err := os.WriteFile(healthy, []byte("healthy"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawStale bool
	stub := func(Runner, string) SuiteResult {
		_, err := os.Stat(stale)
		sawStale = err == nil
		return SuiteResult{Passed: true}
	}
	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "movement"}}
	res, _, acquired := runCargoLocked(stub, r, root, time.Second, time.Second, 0)
	if !acquired || !res.Passed {
		t.Fatalf("expected the lock to be acquired and the stub to run, acquired=%v res=%+v", acquired, res)
	}
	if sawStale {
		t.Fatal("the future-stamped artifact was still present when the command ran — the target dir was not wiped first")
	}
	if _, err := os.Stat(healthy); err == nil {
		t.Fatal("the healthy sibling file survived the wipe — a future-stamped artifact must invalidate the whole target dir, not just itself")
	}
}

// makeFutureArtifact writes one file at target/debug/deps/<name> stamped
// ahead time in the future, creating the directories it needs.
func makeFutureArtifact(t *testing.T, target, name string, ahead time.Duration) {
	t.Helper()
	p := filepath.Join(target, "debug", "deps", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(ahead)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
}

// A box with REAL persistent clock skew (an unsynced VM, a container whose
// clock differs) rebuilds a future-stamped artifact every time this guard
// wipes one: the very next build lands future-stamped again under the same
// wrong clock. Paying the full wipe cost on every run forever is exactly
// the failure this guard exists to avoid — a repair recorded for THIS
// target dir must stop a second wipe from happening while that repair is
// still recent, and say why instead.
func TestInvalidateFutureStampedArtifacts_RefusesASecondWipeWhileARecentRepairStands(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	target := filepath.Join(t.TempDir(), "target")
	makeFutureArtifact(t, target, "libmovement.rmeta", 10*time.Minute)

	invalidateFutureStampedArtifacts(target)
	if _, err := os.Stat(target); err == nil {
		t.Fatal("setup: the first hit must wipe the target dir")
	}
	requireLoggedVerdict(t, cfg, "target-wiped")

	// A fresh process would start with an empty futureMtimeChecked map;
	// deleting THIS target's entry reproduces that without spawning one,
	// and a rebuilt target dir with a NEW future-stamped file is exactly
	// what a still-broken clock produces on the very next run.
	futureMtimeChecked.Delete(target)
	makeFutureArtifact(t, target, "libmovement.rmeta", 10*time.Minute)

	invalidateFutureStampedArtifacts(target)
	if _, err := os.Stat(target); err != nil {
		t.Fatal("a second hit inside the repair window wiped the target dir again — the guard must degrade once and stop, not repeat the full cost every run")
	}
	requireLoggedVerdict(t, cfg, "repeat-no-wipe")
}

// A marker older than futureMtimeMarkerExpiry no longer suppresses a wipe:
// an operator who fixes the clock (or a box whose skew was transient) must
// not stay wedged in "no wipe" state forever just because this guard once
// fired long ago.
func TestInvalidateFutureStampedArtifacts_WipesAgainOnceTheMarkerHasExpired(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	target := filepath.Join(t.TempDir(), "target")
	storeFutureMtimeMarker(target, time.Now().Add(-8*24*time.Hour))
	makeFutureArtifact(t, target, "libmovement.rmeta", 10*time.Minute)

	invalidateFutureStampedArtifacts(target)
	if _, err := os.Stat(target); err == nil {
		t.Fatal("an expired repair marker still suppressed the wipe")
	}
	requireLoggedVerdict(t, cfg, "target-wiped")
}

// An ordinary target dir — every mtime at or before now — must never be
// touched: wiping it on every commit would throw away a warm cache for
// nothing, the exact cost this gate exists to avoid paying.
func TestRunCargoLocked_LeavesAnOrdinaryTargetDirAlone(t *testing.T) {
	withIsolatedBuildLock(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	fresh := filepath.Join(target, "debug", "deps", "libmovement.rmeta")
	if err := os.MkdirAll(filepath.Dir(fresh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawFresh bool
	stub := func(Runner, string) SuiteResult {
		_, err := os.Stat(fresh)
		sawFresh = err == nil
		return SuiteResult{Passed: true}
	}
	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "movement"}}
	if _, _, acquired := runCargoLocked(stub, r, root, time.Second, time.Second, 0); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if !sawFresh {
		t.Fatal("an ordinary target dir was wiped even though nothing in it carried a future mtime")
	}
}
