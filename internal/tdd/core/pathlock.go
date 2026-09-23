package core

import "time"

// A per-path advisory file lock, for a read-modify-write this package needs
// serialized across processes. Two unrelated files each lock their OWN
// path — the mutant outcome store's merge (mutants_store.go, issue #284)
// and the mutation-job registry's own append (mutants_job.go, issue #284
// follow-up) never contend with each other, only two writers of the SAME
// path do.

// acquirePathLock blocks until this process holds the advisory lock for
// path (a lock file at path+".lock"), built on the same TryAcquireFileLock
// primitive as the box-wide mutation-run lock (mutants_runlock.go) — an
// OS-mandatory, per-open-file-description lock that a crashed holder's
// closed descriptor releases on its own.
func acquirePathLock(path string) (release func()) {
	release, _ = acquirePathLockWithDeadline(path, pathLockForever)
	return release
}

// pathLockForever stands in for "no deadline" — every read-modify-write this
// guards is milliseconds, so a caller waiting on it is never abandoning
// anything meaningful; production never reaches the deadline branch below.
const pathLockForever = 365 * 24 * time.Hour

// acquirePathLockWithDeadline is the bounded primitive acquirePathLock
// wraps. Split out, mirroring acquireMutantsRunLockWithDeadline, so a
// contention test can bound the wait without needing an actually-unbounded
// one in the suite itself.
func acquirePathLockWithDeadline(path string, deadline time.Duration) (release func(), ok bool) {
	lockPath := path + ".lock"
	start := time.Now()
	for {
		if rel, acquired := TryAcquireFileLock(lockPath); acquired {
			return rel, true
		}
		if time.Since(start) >= deadline {
			return func() {}, false
		}
		time.Sleep(pathLockPollInterval)
	}
}

// pathLockPollInterval is how often acquirePathLock retries. Short: every
// caller's critical section is fast, so a waiter should not sit out a whole
// poll interval doing nothing after the holder has already finished.
const pathLockPollInterval = 5 * time.Millisecond
