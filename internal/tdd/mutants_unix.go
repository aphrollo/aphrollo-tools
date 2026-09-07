//go:build !windows

package tdd

import (
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
	return p.Signal(syscall.Signal(0)) == nil
}
