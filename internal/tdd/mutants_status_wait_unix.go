//go:build !windows

package tdd

import "time"

// waitForPIDExitPollInterval is how often this falls back to asking the OS
// whether pid still exists. Unlike Windows there is no portable blocking
// process-exit primitive for an ARBITRARY (non-child) pid in the standard
// library — pidfd-style waits are Linux-only and not exposed here — so this
// polls the OS existence check itself. That is still not the thing issue
// #272 was about: nothing here reads a receipt file on a timer, guesses a
// fixed multi-hour deadline, or leaves a caller writing its own loop: one
// call blocks until the process is actually gone.
var waitForPIDExitPollInterval = 500 * time.Millisecond

// waitForPIDExit blocks the caller until pid no longer names a running
// process. POSIX
// has no portable blocking wait for an arbitrary non-child pid, so this
// polls the OS existence check at a short interval instead.
func waitForPIDExit(pid int) {
	if pid <= 0 {
		return
	}
	for pidRunning(pid) {
		time.Sleep(waitForPIDExitPollInterval)
	}
}
