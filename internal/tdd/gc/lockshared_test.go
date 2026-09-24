package gc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// No test here clears the package's lock-dir override: the machine-wide lock
// dir is live while the suite runs (see guardLiveLockDir), so the production
// layout is checked by computing paths, never by resolving that dir.

// TestTargetLockPath_LivesInTheTargetDirItGuards pins the fix for a lock that
// only ever serialised ONE user: keyed under os.TempDir(), a second account or
// a CI runner building the same target/ took its own private lock and the two
// builds trampled each other's build directory. The lock belongs beside the
// thing it guards, where every builder of that directory finds the same file.
// The production path is computed directly: clearing the package's lock-dir
// override would point the rest of the process at the live lock dir.
func TestTargetLockPath_LivesInTheTargetDirItGuards(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")

	got := sharedTargetLockPath(target)
	want := filepath.Join(target, ".aphrollo", "build.lock")
	if got != want {
		t.Fatalf("targetLockPath = %q, want %q", got, want)
	}
}

// TestTargetLock_IsTakeableAndExclusive proves the shared path is a working
// lock and not just a string: the directory is created on demand, and a second
// acquirer of the same target dir is refused.
func TestTargetLock_IsTakeableAndExclusive(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")

	release, ok := TryAcquireFileLock(sharedTargetLockPath(target))
	if !ok {
		t.Fatal("could not take the target lock in a fresh target dir")
	}
	if _, again := TryAcquireFileLock(sharedTargetLockPath(target)); again {
		release()
		t.Fatal("a second acquirer of the same target dir was admitted")
	}
	release()
}

// TestGlobalSlotPath_IsMachineWideNotPerUser pins the other half: the slots are
// the box's OOM governor, so two accounts must contend for the same N. Under
// per-user temp dirs each user got a private set and the box ran 2N builds.
// The candidates are checked, not the resolved dir: resolving it probes the
// live lock dir with a file of its own.
// ratchet: test_removed TestGlobalSlotPath_IsMachineWideNotPerUser: renamed; it resolved the live lock dir to compute the slot path, and the same claim is now made against the candidates that path is built from
func TestSharedLockCandidates_AreMachineWideNotPerUser(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()

	setTempEnv(t, a)
	first := sharedLockCandidates()
	setTempEnv(t, b)
	second := sharedLockCandidates()

	if len(first) == 0 {
		t.Fatal("no machine-wide lock dir candidate on this platform")
	}
	if strings.Join(first, "|") != strings.Join(second, "|") {
		t.Fatalf("lock dir candidates are %q for one user and %q for another — they never contend", first, second)
	}
	for _, dir := range first {
		for _, tmp := range []string{a, b} {
			if strings.HasPrefix(dir, tmp) {
				t.Fatalf("lock dir candidate %s lives in a per-user temp dir (%s)", dir, tmp)
			}
		}
	}
}

// TestGC_StillSweepsTheLitterLeftInTheOldTempLockDir keeps the sweep pointed
// at where the locks USED to live as well as where they live now. Measured
// before the move: 871 aphrollo-cargo-*.lock files in the operator's %TEMP%.
// A sweep that only looks at the new home abandons every one of them.
func TestGC_StillSweepsTheLitterLeftInTheOldTempLockDir(t *testing.T) {
	// Production layout deliberately, but computed rather than resolved:
	// under an override the sweep looks only at the override dir, and
	// clearing the override would sweep the live lock dir.
	shared, legacy := t.TempDir(), t.TempDir()
	dirs := litterDirsFor(shared, false, legacy)
	if len(dirs) != 2 || dirs[0] != shared || dirs[1] != legacy {
		t.Fatalf("litter dirs = %q, want the lock dir and then the old temp dir [%s %s]", dirs, shared, legacy)
	}
	if under := litterDirsFor(shared, true, legacy); len(under) != 1 || under[0] != shared {
		t.Fatalf("litter dirs under a test override = %q, want only the override dir %s", under, shared)
	}

	owner := filepath.Join(legacy, "aphrollo-cargo-build.deadbeef.lock.owner")
	if err := os.WriteFile(owner, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(owner, old, old); err != nil {
		t.Fatal(err)
	}

	for _, c := range gcTempLitter(legacy, GCScope{TempLitter: true}.lockAge(), time.Now()) {
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
