package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLockDir_OneOverrideCoversEveryLockFile pins the isolation seam the
// whole suite depends on. Measured 2026-09-01: the operator's %TEMP% held
// 871 `aphrollo-cargo-build.<key>.lock` files, created in bursts at test-run
// times — every gate test that reached runCargoLocked without setting the
// override wrote its lock, its slot files and its owner record straight into
// the REAL temp dir. One override must move ALL of them, so a package-level
// TestMain can isolate a whole run in one statement — INCLUDING the per-target
// lock, which in production lives inside the target dir it guards
// (TestTargetLockPath_LivesInTheTargetDirItGuards) but under an override
// cannot, because a test names target dirs that do not exist.
func TestLockDir_OneOverrideCoversEveryLockFile(t *testing.T) {
	dir := t.TempDir()
	defer SetLockDirForTest(dir)()

	paths := []string{
		targetLockPath(filepath.Join("D:", "some", "target")),
		globalSlotPath(0),
		ReadBuildSlotOwnerPath(filepath.Join("D:", "some", "target")),
		effectiveBuildLockPath(),
	}
	for _, p := range paths {
		if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(dir)) {
			t.Errorf("%s escapes the overridden lock dir %s", p, dir)
		}
	}
}

// TestLockDir_LeavesNoFilesInTheRealTempDir is the end-to-end statement of
// the same rule: a full acquire/release cycle under the override adds
// nothing to os.TempDir(). This is what the 871 stale files violated.
func TestLockDir_LeavesNoFilesInTheRealTempDir(t *testing.T) {
	before := countTempLocks(t)
	dir := t.TempDir()
	restore := SetLockDirForTest(dir)
	_, release, ok := TryAcquireBuildSlot(filepath.Join(dir, "target"), "cargo test", dir)
	if !ok {
		t.Fatal("could not take an isolated slot")
	}
	release()
	restore()

	if after := countTempLocks(t); after != before {
		t.Fatalf("real temp lock files: %d → %d, want no growth", before, after)
	}
}

// countTempLocks counts aphrollo lock files in the REAL temp dir.
func countTempLocks(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "aphrollo-cargo-") {
			n++
		}
	}
	return n
}
