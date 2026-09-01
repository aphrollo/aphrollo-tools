package tdd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGC_SweepsStaleLockLitter covers the fourth category: the lock files and
// test stub dirs aphrollo itself leaves in the temp dir. Measured 2026-09-01:
// 871 `aphrollo-cargo-build.<key>.lock` files and 65 stub dirs, none of them
// held by anything. Old and unlocked is the bar — a lock somebody currently
// HOLDS is a running build and must survive the sweep.
func TestGC_SweepsStaleLockLitter(t *testing.T) {
	temp := t.TempDir()
	defer SetLockDirForTest(temp)()
	old := time.Now().Add(-48 * time.Hour)

	stale := filepath.Join(temp, "aphrollo-cargo-build.deadbeef.lock")
	owner := stale + ".owner"
	stub := filepath.Join(temp, "aphrollo-cargo-run-stub-123")
	fresh := filepath.Join(temp, "aphrollo-cargo-build.fresh.lock")
	held := filepath.Join(temp, "aphrollo-cargo-build.held.lock")
	for _, p := range []string{stale, owner, fresh, held} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{stale, owner, stub, held} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	release, ok := TryAcquireFileLock(held)
	if !ok {
		t.Fatal("could not hold the lock the sweep must spare")
	}
	defer release()

	got := map[string]bool{}
	for _, c := range ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true}) {
		got[c.Path] = true
	}
	for _, want := range []string{stale, owner, stub} {
		if !got[want] {
			t.Errorf("missed stale litter %s", filepath.Base(want))
		}
	}
	for _, spared := range []string{fresh, held} {
		if got[spared] {
			t.Errorf("proposed %s — a fresh or currently-held lock is a running build", filepath.Base(spared))
		}
	}
}

// TestGC_LockLitterAgeIsTunable pins the knob that makes the category usable
// the day it ships: the litter measured on 2026-09-01 was hours old, so a
// fixed 1-day bar could not clear any of it. An operator who knows no build
// is running lowers the bar; the liveness probe still spares a held lock.
func TestGC_LockLitterAgeIsTunable(t *testing.T) {
	temp := t.TempDir()
	defer SetLockDirForTest(temp)()
	recent := time.Now().Add(-2 * time.Hour)

	lock := filepath.Join(temp, "aphrollo-cargo-build.recent.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(lock, recent, recent); err != nil {
		t.Fatal(err)
	}

	if got := ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true}); len(got) != 0 {
		t.Fatalf("default bar proposed %v, want nothing under a day old", got)
	}
	got := ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true, LockAge: time.Hour})
	if len(got) != 1 || got[0].Path != lock {
		t.Fatalf("with LockAge=1h got %v, want the 2h-old lock", got)
	}
}
