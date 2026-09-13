package tdd

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// withIsolatedBuildLock points acquireBuildLock at a per-test lock file for
// the test's duration, instead of the real machine-wide one. Without this, a
// test exercising the lock races the box's OWN aphrollo PostToolUse hook
// (which runs `go test` against this working tree after every Edit/Write,
// and once installed exercises this exact production lock file too) —
// spurious contention unrelated to the behavior under test. See
// buildLockPathOverride's doc comment.
//
// It also shrinks the PostEdit/Precommit lock-wait deadlines from their
// production values (20s / 300s) to a couple hundred milliseconds: a
// contention test needs the DEADLINE to actually elapse to exercise the
// "gave up waiting" path, and the project's test-quality bar forbids a real
// sleep over 200ms — waiting out a real 20s or 300s budget just to prove a
// timeout path works would violate that outright (and was observed to,
// costing 320s for two tests before this fix).
func withIsolatedBuildLock(t *testing.T) {
	t.Helper()
	restorePath := setBuildLockPathOverride(filepath.Join(t.TempDir(), "test-build.lock"))
	origPostEdit, origPrecommit := buildLockPostEditDeadline, buildLockPrecommitDeadline
	buildLockPostEditDeadline = 120 * time.Millisecond
	buildLockPrecommitDeadline = 150 * time.Millisecond
	t.Cleanup(func() {
		restorePath()
		buildLockPostEditDeadline = origPostEdit
		buildLockPrecommitDeadline = origPrecommit
	})
}

// TestBuildLockPathOverride_ConcurrentSetAndReadIsRaceFree pins the override
// seam as safe for concurrent use. SetBuildLockPathForTest is exported for
// tests in OTHER packages, and `go test` runs packages in parallel: one
// package's restore func writes the override while another's lock acquire
// reads it. CI runs -race, so an unsynchronised global is a hard failure
// there while every local run stays green.
//
// Break this catches: the override becoming a plain read/write global again.
// It fails ONLY under -race — `go test -race -run ConcurrentSetAndRead ./internal/tdd`.
func TestBuildLockPathOverride_ConcurrentSetAndReadIsRaceFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.lock")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			SetBuildLockPathForTest(path)()
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = effectiveBuildLockPath()
		}
	}()
	wg.Wait()
}

// TestAcquireBuildLock_SecondAcquirerBlocksUntilFirstReleases pins the core
// contract: two in-process acquirers of the SAME machine-wide build lock
// serialise — the second cannot acquire while the first holds it, and CAN
// once the first releases. This is what stops several concurrent Claude
// sessions from cold-building the same Bevy workspace at once (the observed
// failure this lock fixes).
func TestAcquireBuildLock_SecondAcquirerBlocksUntilFirstReleases(t *testing.T) {
	withIsolatedBuildLock(t)
	target := t.TempDir()
	_, release1, ok1 := acquireBuildSlot(target, time.Second, "cargo build", "/repo")
	if !ok1 {
		t.Fatal("first acquirer must succeed immediately (uncontended)")
	}

	// Second acquirer, still held by the first: must fail within a SHORT
	// bound — no real sleeps > 200ms in this suite, so the bound itself is
	// deliberately tiny (the poll interval is 20ms, so several polls still
	// fit inside 150ms).
	if _, _, ok2 := acquireBuildSlot(target, 150*time.Millisecond, "cargo build", "/repo"); ok2 {
		t.Fatal("second acquirer must not succeed while the first holds the lock")
	}

	release1()

	// Now that the first released, a fresh attempt must succeed promptly.
	_, release3, ok3 := acquireBuildSlot(target, time.Second, "cargo build", "/repo")
	if !ok3 {
		t.Fatal("a third acquirer must succeed once the lock is released")
	}
	release3()
}

// TestAcquireBuildLock_TimeoutPathReturnsFalseWithinBound pins the timeout
// contract in isolation: acquisition against an already-held lock returns
// false, and does so within roughly the requested deadline — not instantly
// (it must actually have polled) and not by blocking indefinitely.
func TestAcquireBuildLock_TimeoutPathReturnsFalseWithinBound(t *testing.T) {
	withIsolatedBuildLock(t)
	target := t.TempDir()
	_, release, ok := acquireBuildSlot(target, time.Second, "cargo build", "/repo")
	if !ok {
		t.Fatal("setup: first acquirer must succeed")
	}
	defer release()

	const bound = 120 * time.Millisecond
	start := time.Now()
	_, _, ok2 := acquireBuildSlot(target, bound, "cargo build", "/repo")
	elapsed := time.Since(start)

	if ok2 {
		t.Fatal("acquiring an already-held lock must fail")
	}
	// Generous upper bound (10x the requested deadline) so this never flakes
	// on a loaded CI box, while still catching "acquireBuildLock ignored the
	// deadline and blocked much longer" — the bug this test exists to catch.
	if elapsed > bound*10 {
		t.Fatalf("acquireBuildLock took %s against a %s deadline — it is not honoring the bound", elapsed, bound)
	}
}

// TestAcquireBuildLock_ReleaseIsIdempotentSafe guards against a release()
// call panicking or wedging a later acquirer — a defensive double-release
// (e.g. a defer plus an explicit early release on one code path) must be
// harmless.
func TestAcquireBuildLock_ReleaseIsIdempotentSafe(t *testing.T) {
	withIsolatedBuildLock(t)
	target := t.TempDir()
	_, release, ok := acquireBuildSlot(target, time.Second, "cargo build", "/repo")
	if !ok {
		t.Fatal("setup: first acquirer must succeed")
	}
	release()
	release() // must not panic

	_, release2, ok2 := acquireBuildSlot(target, time.Second, "cargo build", "/repo")
	if !ok2 {
		t.Fatal("a later acquirer must still succeed after a double release")
	}
	release2()
}

// TestRunCargoLocked_DeadlineCarvesLockWaitOutOfStageBudget pins the fix for
// a real review finding: before this, the machine-wide lock wait
// (lockDeadline) and the injected SuiteRunner's OWN separate timeout
// (stageBudget) stacked ADDITIVELY — a contended lock could cost up to
// lockDeadline+stageBudget per stage, per root, instead of stageBudget
// total. runCargoLocked now sets r.Deadline = start+stageBudget BEFORE it
// waits for the lock, so a stub SuiteRunner reading r.Deadline sees it
// already eaten into by however long the wait took: the REMAINING time
// (time.Until(r.Deadline), read once the stub actually runs) must be
// approximately stageBudget MINUS the measured wait — never the full,
// un-carved stageBudget.
func TestRunCargoLocked_DeadlineCarvesLockWaitOutOfStageBudget(t *testing.T) {
	withIsolatedBuildLock(t)

	// Hold the lock briefly on another "acquirer" so runCargoLocked's own
	// acquisition is forced to actually wait — a real wait, not an
	// instantaneous uncontended grab, or this test would prove nothing.
	const holdFor = 80 * time.Millisecond
	root := t.TempDir()
	_, release, ok := acquireBuildSlot(resolveTargetDir(os.Getenv, root), time.Second, "cargo nextest run -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the build slot")
	}
	go func() {
		time.Sleep(holdFor)
		release()
	}()

	var gotDeadline time.Time
	stub := func(r Runner, root string) SuiteResult {
		gotDeadline = r.Deadline
		return SuiteResult{Passed: true}
	}

	const stageBudget = 500 * time.Millisecond
	res, waited, acquired := runCargoLocked(stub, Runner{Cmd: "cargo"}, root, time.Second, stageBudget, 0)
	if !acquired || !res.Passed {
		t.Fatalf("expected the lock to be acquired once released, acquired=%v res=%+v", acquired, res)
	}
	if waited < 40*time.Millisecond {
		t.Fatalf("expected the acquirer to have actually waited for the held lock, waited=%s", waited)
	}
	if gotDeadline.IsZero() {
		t.Fatal("stub never saw a Runner.Deadline at all")
	}

	wantRemaining := stageBudget - waited
	gotRemaining := time.Until(gotDeadline)
	// Generous tolerance (scheduling jitter across the goroutine + polling
	// loop), but tight enough to catch "Deadline set AFTER the wait instead
	// of before" (which would show ~stageBudget, not ~stageBudget-waited) —
	// the two are ~80ms apart (holdFor), far bigger than this tolerance.
	const tolerance = 40 * time.Millisecond
	if diff := gotRemaining - wantRemaining; diff > tolerance || diff < -tolerance {
		t.Fatalf("remaining budget at run-time = %s, want ~%s (stageBudget %s - waited %s); diff %s exceeds tolerance %s",
			gotRemaining, wantRemaining, stageBudget, waited, diff, tolerance)
	}
}

// TestRunCargoLocked_GoRaceRunnerTakesTheSameGovernorAsCargo is the cold-review
// finding on #421: a plain `go test` never contended for anything (fine,
// while the Go mechanical stage was cheap), but `-race` makes a Go build
// several times slower and genuinely CPU/RAM-heavy — exactly the box-wide
// contention buildLockPath's own doc comment names for cargo. A `go test
// -race` Runner must now take the SAME governor (target lock + global slot
// pool) a cargo build does, keyed on goRaceLockKey rather than a resolved
// cargo target dir, so concurrent lanes' `-race` runs serialize instead of
// stacking. Proven the same way TestRunCargoLocked_DeadlineCarvesLockWaitOutOfStageBudget
// proves it for cargo: hold the SAME key on another "acquirer", show
// runCargoLocked's own acquisition is forced to actually wait.
func TestRunCargoLocked_GoRaceRunnerTakesTheSameGovernorAsCargo(t *testing.T) {
	withIsolatedBuildLock(t)

	const holdFor = 80 * time.Millisecond
	_, release, ok := acquireBuildSlot(goRaceLockKey(), time.Second, "go test -race ./other/...", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the go-race governor slot")
	}
	go func() {
		// real-time: the OS file lock has no observable hand-off event
		time.Sleep(holdFor)
		release()
	}()

	res, waited, acquired := runCargoLocked(
		func(r Runner, root string) SuiteResult { return SuiteResult{Passed: true} },
		Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "./..."}},
		t.TempDir(), time.Second, 500*time.Millisecond, 0,
	)
	if !acquired || !res.Passed {
		t.Fatalf("expected the go-race runner to acquire the governor once released, acquired=%v res=%+v", acquired, res)
	}
	if waited < 40*time.Millisecond {
		t.Fatalf("expected a go -race runner to actually wait behind the held governor slot, waited=%s — it must not be passing straight through unlocked", waited)
	}
}

// TestRunCargoLocked_PlainGoRunnerStillPassesThroughUnlocked guards the
// unchanged fast path: a `go test` Runner with NO -race (post-edit's plain
// command, fail-first, `go vet`) must never contend for the governor, even
// while its slot is fully held — exactly the pre-#421-review behavior.
func TestRunCargoLocked_PlainGoRunnerStillPassesThroughUnlocked(t *testing.T) {
	withIsolatedBuildLock(t)

	_, release, ok := acquireBuildSlot(goRaceLockKey(), time.Second, "go test -race ./other/...", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the go-race governor slot")
	}
	defer release()

	res, waited, acquired := runCargoLocked(
		func(r Runner, root string) SuiteResult { return SuiteResult{Passed: true} },
		Runner{Cmd: "go", Args: []string{"test", "-count=1", "./..."}},
		t.TempDir(), time.Second, 500*time.Millisecond, 0,
	)
	if !acquired || !res.Passed {
		t.Fatalf("a plain go test runner must never be blocked by the (fully held) go-race governor, acquired=%v res=%+v", acquired, res)
	}
	if waited != 0 {
		t.Fatalf("a plain go test runner must pass straight through with zero wait, waited=%s", waited)
	}
}

// TestRunSuite_HonorsEarlierRunnerDeadline pins the OTHER half of the fix:
// when Runner.Deadline is earlier than RunSuite's own configured timeout,
// RunSuite must actually bound itself to the EARLIER deadline and report
// TimedOut — proving the carved-out budget is enforced, not just computed
// and ignored.
func TestRunSuite_HonorsEarlierRunnerDeadline(t *testing.T) {
	var r Runner
	if runtime.GOOS == "windows" {
		r = Runner{Cmd: "ping", Args: []string{"-n", "30", "127.0.0.1"}}
	} else {
		r = Runner{Cmd: "sleep", Args: []string{"30"}}
	}
	// Much earlier than the 5s configured timeout below.
	const earlyDeadline = 150 * time.Millisecond
	r.Deadline = time.Now().Add(earlyDeadline)

	start := time.Now()
	res := RunSuite(5*time.Second)(r, t.TempDir())
	elapsed := time.Since(start)

	if !res.TimedOut {
		t.Fatal("expected RunSuite to honor the earlier Runner.Deadline and report TimedOut")
	}
	// TimedOut alone doesn't prove the EARLIER deadline was honored — the
	// killed command reports TimedOut whether it was killed at 150ms or at
	// the full 5s configured timeout, so a broken fix (ignoring r.Deadline
	// entirely) would still pass a bare "!res.TimedOut" check by running the
	// FULL 5s. Elapsed time close to earlyDeadline (not anywhere near the 5s
	// configured timeout) is what actually distinguishes the two.
	if elapsed > earlyDeadline+3*time.Second {
		t.Fatalf("RunSuite took %s, want close to the %s Runner.Deadline — it ran to (or near) the full 5s "+
			"configured timeout instead, meaning Runner.Deadline was never honored", elapsed, earlyDeadline)
	}
}
