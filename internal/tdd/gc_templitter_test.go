package tdd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGC_NeverDeletesALockFile is the hard rule of the litter category. A
// lock file IS the mutual exclusion: deleting it while a build holds it
// leaves the holder with a dead inode, and on Windows the delete-pending
// state makes the next open fail — which TryAcquireFileLock reports as
// ACQUIRED. Two builds in one target dir is exactly what the lock exists to
// prevent, so the sweep touches owner records and stub dirs only.
func TestGC_NeverDeletesALockFile(t *testing.T) {
	temp := t.TempDir()
	defer SetLockDirForTest(temp)()
	old := time.Now().Add(-48 * time.Hour)

	lock := filepath.Join(temp, "aphrollo-cargo-build.deadbeef.lock")
	owner := lock + ".owner"
	stub := filepath.Join(temp, "aphrollo-cargo-run-stub-123")
	for _, p := range []string{lock, owner} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{lock, owner, stub} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	got := map[string]bool{}
	for _, c := range ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true}) {
		got[c.Path] = true
	}
	if got[lock] {
		t.Error("proposed a .lock file — deleting one destroys mutual exclusion")
	}
	for _, want := range []string{owner, stub} {
		if !got[want] {
			t.Errorf("missed stale litter %s", filepath.Base(want))
		}
	}
}

// TestGC_SparesTheOwnerOfAHeldLock keeps the sweep honest about running
// builds: an owner record names WHO holds a lock, and a waiting session
// prints it. While the lock is held the record is live, however old it is.
func TestGC_SparesTheOwnerOfAHeldLock(t *testing.T) {
	temp := t.TempDir()
	defer SetLockDirForTest(temp)()
	old := time.Now().Add(-48 * time.Hour)

	lock := filepath.Join(temp, "aphrollo-cargo-build.held.lock")
	owner := lock + ".owner"
	if err := os.WriteFile(owner, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(owner, old, old); err != nil {
		t.Fatal(err)
	}
	release, ok := TryAcquireFileLock(lock)
	if !ok {
		t.Fatal("could not hold the lock the sweep must respect")
	}
	defer release()

	for _, c := range ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true}) {
		if c.Path == owner {
			t.Fatal("proposed the owner record of a lock somebody holds")
		}
	}
}

// TestGC_LockLitterAgeIsTunable pins the knob that makes the category usable
// the day it ships: the litter measured on 2026-09-01 was hours old, so a
// fixed 1-day bar could not clear any of it.
func TestGC_LockLitterAgeIsTunable(t *testing.T) {
	temp := t.TempDir()
	defer SetLockDirForTest(temp)()
	recent := time.Now().Add(-2 * time.Hour)

	owner := filepath.Join(temp, "aphrollo-cargo-build.recent.lock.owner")
	if err := os.WriteFile(owner, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(owner, recent, recent); err != nil {
		t.Fatal(err)
	}

	if got := ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true}); len(got) != 0 {
		t.Fatalf("default bar proposed %v, want nothing under a day old", got)
	}
	got := ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true, LockAge: time.Hour})
	if len(got) != 1 || got[0].Path != owner {
		t.Fatalf("with LockAge=1h got %v, want the 2h-old owner record", got)
	}
}
