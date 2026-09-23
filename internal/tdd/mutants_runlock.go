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
//
// Waiters are served in arrival order (mutants_runqueue.go): each takes a
// ticket first, and only the earliest live ticket tries the lock. The ticket
// is handed back as soon as the wait ends, acquired or not.
func acquireMutantsRunLockWithDeadline(cmd, cwd string, deadline time.Duration) (release func(), ok bool) {
	path := mutantsRunLockPath()
	start := time.Now()
	ticketPath := writeMutantsRunTicket(os.Getpid(), start, cmd, cwd)
	ticket := filepath.Base(ticketPath)
	defer func() { _ = os.Remove(ticketPath) }()
	nextNotice, nextHeartbeat := time.Duration(0), mutantsRunTicketHeartbeat
	for {
		queue := liveMutantsRunQueue(time.Now())
		pos := queuePosition(queue, ticket)
		if pos == 0 {
			// The ticket is gone (a sweep, a disk hiccup): put it back under
			// its original arrival, so the waiter keeps its place.
			writeMutantsRunTicket(os.Getpid(), start, cmd, cwd)
		}
		if pos == 1 {
			if rel, acquired := TryAcquireFileLock(path); acquired {
				writeBuildLockOwnerAt(mutantsRunLockOwnerPath(), cmd, cwd)
				return func() {
					removeBuildLockOwnerAt(mutantsRunLockOwnerPath())
					rel()
				}, true
			}
		}
		waited := time.Since(start)
		if waited >= deadline {
			return func() {}, false
		}
		if waited >= nextHeartbeat {
			now := time.Now()
			_ = os.Chtimes(ticketPath, now, now)
			nextHeartbeat += mutantsRunTicketHeartbeat
		}
		if waited >= nextNotice && pos > 0 {
			fmt.Fprintf(os.Stderr, "gate: queued behind %s for the box-wide mutation-run lock, position %d of %d (waited %.0fs)\n",
				mutantsRunLockHolderDescription(), pos, len(queue), waited.Seconds())
			nextNotice += mutantsRunLockNoticeEvery
		}
		time.Sleep(mutantsRunQueuePollInterval)
	}
}

// mutantsRunLockHolderDescription names the current holder of the box-wide
// mutation-run lock for a waiting acquirer's line, or says its record cannot
// be read — the owner file is best-effort, racy by construction, exactly
// like buildSlotHolderDescription's own, and appends a stale-binary notice
// when the holder's executable is not the one this process runs. A waiter
// behind another waiter can find the lock free for the instant between one
// holder and the next, and says so rather than inventing a holder.
func mutantsRunLockHolderDescription() string {
	if s := snapshotMutantsRunHolder(); s.Held {
		return describeMutantsRunHolder(s)
	}
	return "nobody (the lock is passing to the waiter ahead)"
}

// processExePathFn is the seam a test overrides instead of depending on the
// real OS query, exactly like pidRunningFn/processStartTokenFn: same shape,
// same reason.
var processExePathFn = processExePath

// selfExePathFn is the seam a test overrides with a synthetic path instead
// of depending on the actual test binary's own location.
var selfExePathFn = selfExePath

// selfExePath is this process's own executable path, "" when it cannot be
// read (a permissions problem, an exotic OS) — callers must not build a
// claim on top of that.
func selfExePath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// staleHolderNotice reports what to append to a holder's description when
// its executable is not the one THIS waiting process was started from — the
// deploy-mid-run scenario issue #311 describes: aphrollo's own installer
// renames the running binary aside and lets it keep executing, so a job that
// started before the deploy holds the box-wide lock for as long as its run
// takes, producing results from code that was already replaced. Either side
// unreadable degrades to "" (say nothing), never a claim built on half the
// comparison.
func staleHolderNotice(pid int) string {
	holder, ok := processExePathFn(pid) // best-effort, racy by construction
	if !ok || holder == "" {
		return ""
	}
	mine := selfExePathFn()
	if mine == "" || holder == mine {
		return ""
	}
	return " (running a binary replaced since it started — its results predate this deploy)"
}

// ReplacedBinaryJobsLine names a mutation run that is still executing the
// binary the installer just renamed aside to stalePath. Such a run holds this
// box-wide lock and produces results from code that is no longer installed,
// and INSTALL time is the one moment that fact is free: the installer already
// knows it just replaced the binary, and the lock's own owner record already
// holds the pid (#338). "" — nothing was replaced, nothing is running, or the
// holder is running the current binary — is the common case and stays silent.
func ReplacedBinaryJobsLine(stalePath string) string {
	if stalePath == "" {
		return ""
	}
	o, ok := readBuildLockOwnerAt(mutantsRunLockOwnerPath())
	if !ok {
		return ""
	}
	holder, ok := processExePathFn(o.PID)
	if !ok || holder != stalePath {
		return ""
	}
	return fmt.Sprintf("gate: a mutation run (pid %d, in %s) is still executing the binary just replaced (%s) — its results predate this install",
		o.PID, o.Cwd, stalePath)
}
