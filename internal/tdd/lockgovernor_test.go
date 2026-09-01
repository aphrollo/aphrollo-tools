package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTryAcquireFileLock_FailsClosed pins the direction of the fail: a lock
// file that cannot even be OPENED (a full disk, a permission problem, a
// delete-pending name) used to report ACQUIRED, which is the one answer that
// cannot be recovered from — every waiting build walks straight in, and the
// sweep deletes under them. Not being able to lock means not acquired.
func TestTryAcquireFileLock_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	// A directory cannot be opened as a lock FILE on any platform.
	blocked := filepath.Join(dir, "unopenable.lock")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}

	release, ok := TryAcquireFileLock(blocked)
	release()
	if ok {
		t.Fatal("an unopenable lock file reported ACQUIRED — every concurrent build would be admitted")
	}
}

// TestEnvWithBuildJobs_NeverRaisesTheCallersCap pins the governor's job: a
// lane shell exports CARGO_BUILD_JOBS, and yielding to it disengaged the cap
// entirely — N concurrent slots each linking with the whole box's job count
// is the OOM this exists to prevent.
func TestEnvWithBuildJobs_NeverRaisesTheCallersCap(t *testing.T) {
	got := EnvWithBuildJobs([]string{"CARGO_BUILD_JOBS=15"}, 7)
	if want := "CARGO_BUILD_JOBS=7"; !containsEnv(got, want) {
		t.Fatalf("env = %v, want the slot's cap %q to win over the caller's 15", got, want)
	}
	got = EnvWithBuildJobs([]string{"CARGO_BUILD_JOBS=2"}, 7)
	if want := "CARGO_BUILD_JOBS=2"; !containsEnv(got, want) {
		t.Fatalf("env = %v, want the caller's STRICTER %q kept", got, want)
	}
	if n := countEnv(EnvWithBuildJobs([]string{"CARGO_BUILD_JOBS=15"}, 7), "CARGO_BUILD_JOBS"); n != 1 {
		t.Fatalf("CARGO_BUILD_JOBS appears %d times, want exactly one", n)
	}
}

func containsEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func countEnv(env []string, key string) int {
	n := 0
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok && k == key {
			n++
		}
	}
	return n
}

// TestLockSeams_AreOne pins that there is exactly ONE isolation seam. Two
// overlapping overrides (a lock PATH and a lock DIR) meant a test could set
// the one that does not cover the paths it actually touches and write into
// the operator's real temp dir believing it was isolated: the path seam moves
// every artefact, and restoring it returns every artefact.
func TestLockSeams_AreOne(t *testing.T) {
	home := t.TempDir()
	defer SetLockDirForTest(home)()
	away := t.TempDir()

	restore := SetBuildLockPathForTest(filepath.Join(away, "custom.lock"))
	for _, p := range []string{effectiveBuildLockPath(), globalSlotPath(0), targetLockPath(filepath.Join(away, "t"))} {
		if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(away)) {
			t.Fatalf("%s escaped the overridden location", p)
		}
	}
	restore()
	for _, p := range []string{effectiveBuildLockPath(), globalSlotPath(0), targetLockPath(filepath.Join(home, "t"))} {
		if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(home)) {
			t.Fatalf("%s did not come back to the outer override — the two seams are still separate", p)
		}
	}
}
