package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A consuming repo's nextest config can give a wall-clock test
// `threads-required = "num-cpus"` — a declaration that ONE run already needs
// every thread on the box, because that is what a real-time measurement
// needs to mean anything. Four such runs together do not share the box:
// they each get a quarter of it and every one of them measures wrong,
// landing on whichever wall-clock test has the least headroom at that
// moment (issue #253). The SAME oversubscription costs memory too, not just
// threads — several lanes cold-building the same crates at once OOM'd
// rustc mid-build.
//
// The fix mirrors buildLockPath/buildslots.go one level up: instead of
// serialising builds into one target dir, this serialises whole MUTATION
// RUNS across the box — one at a time, covering the run's own build AND its
// test phase, because both are the resource the config already says the run
// needs entirely. It reuses the exact primitives the build lock is built
// from (TryAcquireFileLock, the machine-wide lockDir, the owner-file
// naming), so it inherits the same fail-closed and stale-reclamation
// behaviour rather than inventing a second policy.

// mutantsRunLockPath is the machine-wide advisory lock a mutation RUN holds
// for its whole duration — from before the producer's own build starts to
// after its last mutant is judged. One file, contended by every account and
// every repo on the box, beside the build lock in the same shared directory.
func mutantsRunLockPath() string {
	return filepath.Join(lockDir(), "aphrollo-mutants-run.lock")
}

// mutantsRunLockOwnerPath is the owner record beside the lock, read the same
// way buildSlotHolderDescription reads a build slot's: best-effort, so a
// waiter can NAME the holder rather than reporting a bare "someone else has
// it".
func mutantsRunLockOwnerPath() string {
	return mutantsRunLockPath() + ".owner"
}

// mutantsRunLockNoticeEvery is how often a queued run says who it is waiting
// for. A run willing to wait for a cold multi-crate build plus a full mutant
// sweep — potentially hours — must not do that in silence, and must not
// repeat itself every poll either. A `var`, not a `const`, only so a
// contention test can shrink it; production never assigns to it.
var mutantsRunLockNoticeEvery = 60 * time.Second

// mutantsRunLockForever stands in for "no deadline": production always calls
// acquireMutantsRunLock, which passes this. It is long enough that "wait
// forever" and "wait this long" are indistinguishable in practice (a
// mutation run is minutes to hours; this is a year), while still routing
// through the identical, independently-tested poll loop a contention test
// bounds with a short deadline of its own. This is deliberate: issue #253
// asks for NO timeout that abandons the wait and runs anyway, so production
// never actually reaches the deadline branch below.
const mutantsRunLockForever = 365 * 24 * time.Hour

// acquireMutantsRunLock blocks until this process holds the box-wide
// mutation-run lock, announcing the holder every mutantsRunLockNoticeEvery
// while it waits. There is deliberately no way for a caller to give up: a
// run that abandoned the wait and went ahead unlocked is exactly the 4:1
// oversubscription issue #253 reports.
func acquireMutantsRunLock(cmd, cwd string) (release func()) {
	release, _ = acquireMutantsRunLockWithDeadline(cmd, cwd, mutantsRunLockForever)
	return release
}

// acquireMutantsRunLockWithDeadline is the bounded primitive
// acquireMutantsRunLock wraps. Split out so a contention test can bound the
// wait without needing an actually-unbounded wait in the suite itself —
// mirrors acquireBuildSlot's own deadline parameter for the identical
// reason.
func acquireMutantsRunLockWithDeadline(cmd, cwd string, deadline time.Duration) (release func(), ok bool) {
	path := mutantsRunLockPath()
	start := time.Now()
	nextNotice := mutantsRunLockNoticeEvery
	for {
		if rel, acquired := TryAcquireFileLock(path); acquired {
			writeBuildLockOwnerAt(mutantsRunLockOwnerPath(), cmd, cwd)
			return func() {
				removeBuildLockOwnerAt(mutantsRunLockOwnerPath())
				rel()
			}, true
		}
		waited := time.Since(start)
		if waited >= deadline {
			return func() {}, false
		}
		if waited >= nextNotice {
			fmt.Fprintf(os.Stderr, "gate: queued behind %s for the box-wide mutation-run lock (waited %.0fs)\n",
				mutantsRunLockHolderDescription(), waited.Seconds())
			nextNotice += mutantsRunLockNoticeEvery
		}
		time.Sleep(buildLockPollInterval)
	}
}

// mutantsRunLockHolderDescription names the current holder of the box-wide
// mutation-run lock for a waiting acquirer's line, or says the holder is
// unknown — the owner file is best-effort, racy by construction, exactly
// like buildSlotHolderDescription's own.
func mutantsRunLockHolderDescription() string {
	if o, ok := readBuildLockOwnerAt(mutantsRunLockOwnerPath()); ok {
		return describeOwner(o)
	}
	return "another mutation run (holder unknown)"
}
