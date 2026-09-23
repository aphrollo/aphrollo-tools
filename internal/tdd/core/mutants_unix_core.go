//go:build !windows

package core

import (
	"errors"
	"os"
	"syscall"
)

// pidRunning reports whether a process with this pid exists. Signal 0 is the
// portable existence probe: it performs no delivery, only the permission and
// existence checks.
func pidRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// EPERM is the answer for another account's process: the kernel found
	// it and refused the signal, which is proof it exists. The locks are
	// shared with the CI runner's account, so reading that refusal as "dead"
	// drops every holder and waiter that is not this user's own.
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
