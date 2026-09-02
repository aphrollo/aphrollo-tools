package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTargetLockPath_LivesInTheTargetDirItGuards pins the fix for a lock that
// only ever serialised ONE user: keyed under os.TempDir(), a second account or
// a CI runner building the same target/ took its own private lock and the two
// builds trampled each other's build directory. The lock belongs beside the
// thing it guards, where every builder of that directory finds the same file.
func TestTargetLockPath_LivesInTheTargetDirItGuards(t *testing.T) {
	defer SetLockDirForTest("")()
	target := filepath.Join(t.TempDir(), "target")

	got := targetLockPath(target)
	want := filepath.Join(target, ".aphrollo", "build.lock")
	if got != want {
		t.Fatalf("targetLockPath = %q, want %q", got, want)
	}
}

// TestTargetLock_IsTakeableAndExclusive proves the shared path is a working
// lock and not just a string: the directory is created on demand, and a second
// acquirer of the same target dir is refused.
func TestTargetLock_IsTakeableAndExclusive(t *testing.T) {
	defer SetLockDirForTest("")()
	target := filepath.Join(t.TempDir(), "target")

	release, ok := TryAcquireFileLock(targetLockPath(target))
	if !ok {
		t.Fatal("could not take the target lock in a fresh target dir")
	}
	if _, again := TryAcquireFileLock(targetLockPath(target)); again {
		release()
		t.Fatal("a second acquirer of the same target dir was admitted")
	}
	release()
}

// TestGlobalSlotPath_IsMachineWideNotPerUser pins the other half: the slots are
// the box's OOM governor, so two accounts must contend for the same N. Under
// per-user temp dirs each user got a private set and the box ran 2N builds.
func TestGlobalSlotPath_IsMachineWideNotPerUser(t *testing.T) {
	defer SetLockDirForTest("")()
	a, b := t.TempDir(), t.TempDir()

	setTempEnv(t, a)
	first := globalSlotPath(0)
	setTempEnv(t, b)
	second := globalSlotPath(0)

	if first != second {
		t.Fatalf("slot 0 is %q for one user and %q for another — they never contend", first, second)
	}
	for _, tmp := range []string{a, b} {
		if strings.HasPrefix(first, tmp) {
			t.Fatalf("slot 0 lives in a per-user temp dir (%s): %s", tmp, first)
		}
	}
	if sharedLockDir() == os.TempDir() {
		t.Fatalf("no machine-wide lock dir is writable on this box; slots fell back to %s", os.TempDir())
	}
}

// TestGC_StillSweepsTheLitterLeftInTheOldTempLockDir keeps the sweep pointed
// at where the locks USED to live as well as where they live now. Measured
// before the move: 871 aphrollo-cargo-*.lock files in the operator's %TEMP%.
// A sweep that only looks at the new home abandons every one of them.
func TestGC_StillSweepsTheLitterLeftInTheOldTempLockDir(t *testing.T) {
	// Production layout deliberately: under an override the sweep looks only
	// at the override dir, so the legacy fallback would never be exercised.
	defer SetLockDirForTest("")()
	legacy := t.TempDir()
	setTempEnv(t, legacy)

	owner := filepath.Join(legacy, "aphrollo-cargo-build.deadbeef.lock.owner")
	if err := os.WriteFile(owner, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(owner, old, old); err != nil {
		t.Fatal(err)
	}

	for _, c := range ScanGC(t.TempDir(), DefaultGCAge, GCScope{TempLitter: true}) {
		if c.Path == owner {
			return
		}
	}
	t.Fatalf("stale litter in the old temp lock dir was not proposed: %s", owner)
}

// setTempEnv points every variable os.TempDir consults at dir, simulating a
// second account whose temp dir is its own.
func setTempEnv(t *testing.T, dir string) {
	t.Helper()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, dir)
	}
}
