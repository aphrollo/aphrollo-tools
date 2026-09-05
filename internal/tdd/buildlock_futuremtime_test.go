package tdd

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
	res, _, acquired := runCargoLocked(stub, r, root, time.Second, time.Second)
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
	if _, _, acquired := runCargoLocked(stub, r, root, time.Second, time.Second); !acquired {
		t.Fatal("expected the lock to be acquired")
	}
	if !sawFresh {
		t.Fatal("an ordinary target dir was wiped even though nothing in it carried a future mtime")
	}
}
