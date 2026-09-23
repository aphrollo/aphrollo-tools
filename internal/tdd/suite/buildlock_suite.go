package suite

import (
	"os"
	"time"
)

// hasRaceFlag reports whether a `go test` Runner carries -race — the one
// flag on the Go path expensive enough to need runCargoLocked's governor.
// Every other go invocation (post-edit's plain command, fail-first, `go
// vet`, the linter) is untouched by this and keeps passing straight
// through unlocked, exactly as before #421's CI-parity flags existed.
func hasRaceFlag(r Runner) bool {
	if r.Cmd != "go" {
		return false
	}
	for _, a := range r.Args {
		if a == "-race" {
			return true
		}
	}
	return false
}

// logLockWait records a build-slot wait long enough to explain a slow gate
// run, IN ADDITION to whatever verdict the stage itself reaches.
func logLockWait(gateName, root string, r Runner, waited time.Duration) {
	if waited < lockWaitLogAfter() {
		return
	}
	AppendGateLog(gateName, root, cmdString(r), "lock-wait", waited)
}

// runCargoLocked wraps a SuiteRunner invocation with the machine-wide build
// lock: a cargo runner always takes it, and a `go test -race` runner takes
// it too (see hasRaceFlag) — every OTHER runner (plain go, pytest, vitest;
// none saturates the box the way an uncapped cargo build or a `-race`
// compile does) passes straight through, untouched, regardless of who holds
// the lock. The lock is held ONLY for the duration of THIS run — acquired
// immediately before, released immediately after — never across stages, and
// never across a whole Precommit call that spans multiple project roots.
// acquired=false (and a zero-value res) means the deadline elapsed before
// the lock came free; the caller reports that as a distinct QUEUED-SKIPPED
// outcome, never as a timeout (a stopwatch verdict on this project's suite)
// or a failure.
//
// stageBudget is the OVERALL time this call may spend, lock-wait AND suite
// run combined — NOT additional to it. Found in review: with only
// lockDeadline bounding the wait and the injected SuiteRunner carrying its
// OWN separate timeout (e.g. 600s for precommit), a contended lock made the
// two stack additively (300s wait + 600s run = up to 900s per stage, per
// root). r.Deadline is set to start+stageBudget BEFORE the wait begins, so
// by the time run() actually executes, RunSuite sees a Deadline already
// eaten into by however long the wait took, and bounds itself to whichever
// is shorter: its own configured timeout, or the time remaining until
// Deadline.
//
// floor is how much of that budget the wait may NOT eat: the time this suite
// is recorded to need (budgetfloor.go), or zero for a caller with no record
// to stand on, which leaves the arithmetic above exactly as it was. Clamped
// to stageBudget before it is applied, so a floor can give back a budget the
// queue shrank and can never lift the ceiling off a suite that hangs.
func runCargoLocked(run SuiteRunner, r Runner, root string, lockDeadline, stageBudget, floor time.Duration) (res SuiteResult, waited time.Duration, acquired bool) {
	racy := hasRaceFlag(r)
	if r.Cmd != "cargo" && !racy {
		return run(r, root), 0, true
	}
	start := time.Now()
	r.Deadline = start.Add(stageBudget)
	dir := root
	if r.Dir != "" {
		dir = r.Dir
	}
	target := goRaceLockKey()
	if !racy {
		target = runnerTargetDir(r, root)
	}
	slot, release, ok := acquireBuildSlot(target, lockDeadline, cmdString(r), dir)
	waited = time.Since(start)
	if !ok {
		return SuiteResult{}, waited, false
	}
	defer release()
	// The queue decided WHETHER this run could start; it does not get to
	// decide how long the work takes (issue #660). Whatever the wait left,
	// the run gets at least the floor its own record justifies.
	if remaining := cappedFloor(floor, stageBudget); remaining > time.Until(r.Deadline) {
		r.Deadline = time.Now().Add(remaining)
	}
	if !racy {
		defer setBuildJobs(slot.Jobs)()
		// The target lock above is exclusive per target dir, so nothing else
		// can be writing into target while this runs — see
		// buildlock_futuremtime.go. goRaceLockKey names no real directory, so
		// this has nothing to scan for a `-race` run.
		invalidateFutureStampedArtifacts(target)
	}

	// A nested cargo invocation (task A7's cargo-queue shim, IF a session
	// prepended it to PATH) that happens to resolve "cargo" to the shim
	// instead of the real binary must recognize the lock is ALREADY held by
	// THIS process and pass straight through, or it deadlocks on the same
	// lock — true whether THIS process is holding it for a cargo build or
	// for a `-race` Go run, since both draw from the same global slot pool.
	// suiteEnv() inherits os.Environ(), so setting this in the process's own
	// environment is what actually propagates it to the child.
	prevHeld, hadHeld := os.LookupEnv(BuildLockHeldEnv)
	os.Setenv(BuildLockHeldEnv, "1")
	defer func() {
		if hadHeld {
			os.Setenv(BuildLockHeldEnv, prevHeld)
		} else {
			os.Unsetenv(BuildLockHeldEnv)
		}
	}()

	return run(r, root), waited, true
}
