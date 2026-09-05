package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withIsolatedMutantsRunLock points every lock this package owns (there is
// exactly one seam, see TestLockSeams_AreOne) at a per-test directory, the
// same way withIsolatedBuildLock isolates the build lock: without it a test
// here would contend with the box's own real mutation-run lock file, or with
// the aphrollo PostToolUse hook's own cargo run exercising the SAME lock this
// very edit is adding.
func withIsolatedMutantsRunLock(t *testing.T) {
	t.Helper()
	restore := SetLockDirForTest(t.TempDir())
	t.Cleanup(restore)
}

// TestAcquireMutantsRunLock_SecondAcquirerCannotAcquireWhileFirstHolds pins
// the core contract issue #253 asks for: a consuming repo's nextest config
// can give a wall-clock test `threads-required = "num-cpus"`, which is a
// declaration that ONE mutation run already needs the whole box. A second
// run must not be admitted while the first holds the box-wide lock.
func TestAcquireMutantsRunLock_SecondAcquirerCannotAcquireWhileFirstHolds(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	release1 := acquireMutantsRunLock("cargo-mutants for /repo/a", "/repo/a")
	defer release1()

	if _, ok := acquireMutantsRunLockWithDeadline("cargo-mutants for /repo/b", "/repo/b", 150*time.Millisecond); ok {
		t.Fatal("a second mutation run must not acquire the box-wide lock while the first holds it")
	}
}

// TestAcquireMutantsRunLock_AcquiresOnceFirstReleases pins the other half:
// once the first run's lock is released, a queued run gets it promptly
// rather than staying refused forever.
func TestAcquireMutantsRunLock_AcquiresOnceFirstReleases(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	release1 := acquireMutantsRunLock("cargo-mutants for /repo/a", "/repo/a")

	if _, ok := acquireMutantsRunLockWithDeadline("cargo-mutants for /repo/b", "/repo/b", 150*time.Millisecond); ok {
		t.Fatal("setup: the second acquirer must not succeed while the first still holds the lock")
	}

	release1()

	release2, ok := acquireMutantsRunLockWithDeadline("cargo-mutants for /repo/b", "/repo/b", time.Second)
	if !ok {
		t.Fatal("a queued mutation run must acquire the lock once the first releases it")
	}
	release2()
}

// TestAcquireMutantsRunLock_StaleHolderReclaimedWhenItsDescriptorCloses pins
// the reclamation policy this lock REUSES rather than invents: an
// flock/LockFileEx lock is bound to the OPEN FILE DESCRIPTION, so the OS
// itself drops it the instant the holder's descriptor closes — whether that
// close came from an explicit release() or a crashed process's handles being
// torn down by the OS. There is no PID check and no age heuristic here:
// buildlock_unix.go/buildlock_windows.go already document that this is the
// contract every other lock in this package relies on, and this lock is
// built on the identical TryAcquireFileLock/openLockFile/tryLockExclusive
// primitives for exactly that reason.
func TestAcquireMutantsRunLock_StaleHolderReclaimedWhenItsDescriptorCloses(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	path := mutantsRunLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	f, err := openLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !tryLockExclusive(f) {
		t.Fatal("setup: could not take the lock directly")
	}

	if _, ok := acquireMutantsRunLockWithDeadline("second", "/repo", 150*time.Millisecond); ok {
		t.Fatal("setup: the lock must read as held while the stale holder's descriptor is still open")
	}

	f.Close() // simulates the holder dying: no unlock call, only the fd going away

	release, ok := acquireMutantsRunLockWithDeadline("second", "/repo", time.Second)
	if !ok {
		t.Fatal("closing the stale holder's descriptor must free the lock for a new acquirer")
	}
	release()
}

// TestAcquireMutantsRunLock_AnnouncesTheQueueWhileItWaits pins visibility: a
// run willing to wait an unbounded time for the box must say who it is
// waiting for while it waits, not only if it ever gives up — a silent wait is
// indistinguishable from a hang.
func TestAcquireMutantsRunLock_AnnouncesTheQueueWhileItWaits(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	release := acquireMutantsRunLock("cargo-mutants for /repo/holder", "/repo/holder")
	defer release()

	prevNotice := mutantsRunLockNoticeEvery
	mutantsRunLockNoticeEvery = 30 * time.Millisecond
	t.Cleanup(func() { mutantsRunLockNoticeEvery = prevNotice })

	stderr := captureStderr(t, func() {
		acquireMutantsRunLockWithDeadline("cargo-mutants for /repo/waiter", "/repo/waiter", 120*time.Millisecond)
	})
	if !strings.Contains(stderr, "queued behind") || !strings.Contains(stderr, "/repo/holder") {
		t.Fatalf("a waiting mutation run must name the holder while it waits, got: %q", stderr)
	}
}

// TestAcquireMutantsRunLock_NamesStaleHolderWhenBinaryWasReplaced pins issue
// #311: aphrollo's own self-install renames the running binary aside and
// leaves it executing, so a job that started before a deploy keeps holding
// the box-wide lock under a binary that no longer exists on disk under that
// name. Its results were produced by different code, and a waiter reading
// only "queued behind X" has no way to know that -- the queue line has to
// say so itself, at the moment someone is waiting on it.
func TestAcquireMutantsRunLock_NamesStaleHolderWhenBinaryWasReplaced(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	release := acquireMutantsRunLock("cargo-mutants for /repo/holder", "/repo/holder")
	defer release()

	prevExe, prevSelf := processExePathFn, selfExePathFn
	processExePathFn = func(pid int) (string, bool) { return `C:\bin\aphrollo.stale-1788569851.exe`, true }
	selfExePathFn = func() string { return `C:\bin\aphrollo.exe` }
	t.Cleanup(func() { processExePathFn, selfExePathFn = prevExe, prevSelf })

	prevNotice := mutantsRunLockNoticeEvery
	mutantsRunLockNoticeEvery = 30 * time.Millisecond
	t.Cleanup(func() { mutantsRunLockNoticeEvery = prevNotice })

	stderr := captureStderr(t, func() {
		acquireMutantsRunLockWithDeadline("cargo-mutants for /repo/waiter", "/repo/waiter", 120*time.Millisecond)
	})
	if !strings.Contains(stderr, "queued behind") || !strings.Contains(stderr, "replaced") {
		t.Fatalf("a waiter behind a holder running a replaced binary must say so, got: %q", stderr)
	}
}

// TestAcquireMutantsRunLock_SaysNothingWhenHolderRunsTheSameBinary pins the
// other half: a holder whose executable matches the waiter's own gets no
// stale notice appended -- the common case, where nothing has been deployed
// between the holder starting and the waiter queueing.
func TestAcquireMutantsRunLock_SaysNothingWhenHolderRunsTheSameBinary(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	release := acquireMutantsRunLock("cargo-mutants for /repo/holder", "/repo/holder")
	defer release()

	prevExe, prevSelf := processExePathFn, selfExePathFn
	processExePathFn = func(pid int) (string, bool) { return `C:\bin\aphrollo.exe`, true }
	selfExePathFn = func() string { return `C:\bin\aphrollo.exe` }
	t.Cleanup(func() { processExePathFn, selfExePathFn = prevExe, prevSelf })

	prevNotice := mutantsRunLockNoticeEvery
	mutantsRunLockNoticeEvery = 30 * time.Millisecond
	t.Cleanup(func() { mutantsRunLockNoticeEvery = prevNotice })

	stderr := captureStderr(t, func() {
		acquireMutantsRunLockWithDeadline("cargo-mutants for /repo/waiter", "/repo/waiter", 120*time.Millisecond)
	})
	if strings.Contains(stderr, "replaced") {
		t.Fatalf("a holder running the same binary as the waiter must get no stale notice, got: %q", stderr)
	}
}

// TestStaleHolderNotice_SaysNothingWhenEitherSideIsUnreadable pins the
// degrade-to-silence rule: a comparison that cannot be made must never
// produce a claim, in either direction of failure -- the holder's exe path
// unreadable, or this process's own.
func TestStaleHolderNotice_SaysNothingWhenEitherSideIsUnreadable(t *testing.T) {
	prevExe, prevSelf := processExePathFn, selfExePathFn
	defer func() { processExePathFn, selfExePathFn = prevExe, prevSelf }()

	processExePathFn = func(pid int) (string, bool) { return "", false }
	selfExePathFn = func() string { return `C:\bin\aphrollo.exe` }
	if got := staleHolderNotice(4321); got != "" {
		t.Fatalf("an unreadable holder path must produce no notice, got %q", got)
	}

	processExePathFn = func(pid int) (string, bool) { return `C:\bin\aphrollo.stale-1.exe`, true }
	selfExePathFn = func() string { return "" }
	if got := staleHolderNotice(4321); got != "" {
		t.Fatalf("an unreadable self path must produce no notice, got %q", got)
	}
}

// TestRunMutantsJob_HoldsTheMutantsRunLockForTheWholeProducerInvocation pins
// the wiring: the box-wide lock has to cover the run's own BUILD as well as
// its test phase (a cold `cargo mutants` baseline build was separately
// observed OOMing when several lanes built the same crates at once), so
// RunMutantsJob must hold it around the WHOLE producer call — build and test
// together — and release it once that call returns. The fake producer probes
// the SAME lock reentrantly and synchronously, so this proves the hold
// without any goroutine or real-time wait.
func TestRunMutantsJob_HoldsTheMutantsRunLockForTheWholeProducerInvocation(t *testing.T) {
	withIsolatedMutantsRunLock(t)
	root := optedInLane(t)
	j := laneJob(t, root)

	prev := mutantsProducerFn
	var lockWasHeldDuringProducer bool
	mutantsProducerFn = func(j MutantsJob, judged []MutantOutcome) int {
		_, ok := acquireMutantsRunLockWithDeadline("reentrant probe", "/probe", 50*time.Millisecond)
		lockWasHeldDuringProducer = !ok
		return 0
	}
	t.Cleanup(func() { mutantsProducerFn = prev })

	RunMutantsJob(writeJobFile(t, j))

	if !lockWasHeldDuringProducer {
		t.Fatal("RunMutantsJob must hold the box-wide mutation-run lock for the whole producer invocation")
	}

	release, ok := acquireMutantsRunLockWithDeadline("after", "/after", 150*time.Millisecond)
	if !ok {
		t.Fatal("RunMutantsJob must release the box-wide mutation-run lock once the producer returns")
	}
	release()
}
