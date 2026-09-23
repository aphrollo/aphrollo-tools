//go:build unix

package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The box-wide locks are shared by the local sessions and the CI runner's
// account. A holder record only one of them can read is a holder the other
// cannot see: `aphrollo status` showed no mutation run at all while a CI job
// had held the lock for forty minutes, because the owner record was 0600 and
// belonged to the runner. These tests pin the cross-account half of every
// holder and waiter record, and they are unix statements: Windows files
// inherit the directory ACL and carry no meaningful mode bits.

// TestHolderRecords_AreReadableAndRewritableByEveryAccount pins the mode of
// each record a lock writes beside itself. 0666 like the lock file, not
// merely world-readable: the lock directory is sticky, so a record another
// account left behind can only ever be replaced by truncating it in place.
func TestHolderRecords_AreReadableAndRewritableByEveryAccount(t *testing.T) {
	restore := SetLockDirForTest(t.TempDir())
	defer restore()

	release := acquireMutantsRunLock("mutants measure for /repo/a", "/repo/a")
	defer release()
	removeWaiter := WriteQueueWaiter("/repo/b/target", "cargo build", "/repo/b")
	defer removeWaiter()
	writeBuildLockOwnerAt(globalSlotOwnerPath(0), "cargo test", "/repo/c")

	records := map[string]string{
		"mutation-run owner record": mutantsRunLockOwnerPath(),
		"build-slot owner record":   globalSlotOwnerPath(0),
		"queue-waiter record":       queueWaiterPath("/repo/b/target", os.Getpid()),
	}
	for name, path := range records {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := fi.Mode().Perm(); got != 0o666 {
			t.Errorf("%s %s has mode %04o, want 0666 so every account sharing the lock can read and replace it", name, filepath.Base(path), got)
		}
	}

	fi, err := os.Stat(queueWaitersDir())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o777 || fi.Mode()&os.ModeSticky == 0 {
		t.Errorf("queue-waiters dir has mode %v, want drwxrwxrwt so another account can enqueue in it", fi.Mode())
	}
}

// TestTryAcquireFileLock_CreatesItsDirectoryForEveryAccount pins the per-target
// lock's own directory: the first builder of a fresh target creates it, and a
// directory the umask left 0775 is one the next account can neither create its
// lock file in nor write its owner record into.
func TestTryAcquireFileLock_CreatesItsDirectoryForEveryAccount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "target", ".aphrollo")
	release, ok := TryAcquireFileLock(filepath.Join(dir, "build.lock"))
	if !ok {
		t.Fatal("could not take a fresh lock")
	}
	release()

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o777 || fi.Mode()&os.ModeSticky == 0 {
		t.Errorf("lock dir has mode %v, want drwxrwxrwt so another account can lock and record itself in it", fi.Mode())
	}
}

// TestPidRunning_AnotherAccountsLiveProcessIsRunning pins the liveness half:
// signalling another account's process is refused with EPERM, and a refusal
// is proof the process exists. Reading it as "dead" dropped every CI-held
// build slot and every CI waiter from a local session's report.
func TestPidRunning_AnotherAccountsLiveProcessIsRunning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may signal every process, so no refusal can be provoked") // skip-ok: environment probe, not a disabled assertion
	}
	if !pidRunning(1) {
		t.Fatal("pid 1 belongs to root and is alive; pidRunning(1) = false reads a permission refusal as a dead process")
	}
}

// TestMutantsRunStatus_UnreadableOwnerRecordReadsAsHeld pins what status says
// when the lock is held and the record naming the holder cannot be read (an
// older binary wrote it 0600 under another account): the lock is held, and
// the report must say so rather than print nothing.
func TestMutantsRunStatus_UnreadableOwnerRecordReadsAsHeld(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0000 file, so the unreadable case cannot be built") // skip-ok: environment probe, not a disabled assertion
	}
	restore := SetLockDirForTest(t.TempDir())
	defer restore()
	release := acquireMutantsRunLock("mutants measure for /repo/a", "/repo/a")
	defer release()
	if err := os.Chmod(mutantsRunLockOwnerPath(), 0); err != nil {
		t.Fatal(err)
	}

	out := FormatMutantsRunStatus(SnapshotMutantsRun(), time.Now())

	if !strings.Contains(out, "held by an unreadable owner") {
		t.Fatalf("a held lock whose owner record is unreadable must read as held, got:\n%s", out)
	}
}

// TestMutantsRunStatus_NamesTheHolderAndReadsIdleOnceReleased pins the two
// ordinary states of the same report line.
func TestMutantsRunStatus_NamesTheHolderAndReadsIdleOnceReleased(t *testing.T) {
	restore := SetLockDirForTest(t.TempDir())
	defer restore()
	release := acquireMutantsRunLock("mutants measure for /repo/a", "/repo/a")

	held := FormatMutantsRunStatus(SnapshotMutantsRun(), time.Now())
	release()
	idle := FormatMutantsRunStatus(SnapshotMutantsRun(), time.Now())

	if !strings.Contains(held, "held by") || !strings.Contains(held, "/repo/a") {
		t.Errorf("a held lock must name its holder, got:\n%s", held)
	}
	if !strings.Contains(idle, "idle") || strings.Contains(idle, "held by") {
		t.Errorf("a released lock must read idle, got:\n%s", idle)
	}
}
