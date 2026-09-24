//go:build !windows

package workspace

import (
	"errors"
	"os"
	"syscall"
)

// gatePRMergeHolderAlive reports whether a process with this pid exists.
// Signal 0 is the portable existence probe: it performs no delivery, only
// the permission and existence checks. Matches internal/tdd/core's own
// pidRunning (mutants_unix_core.go) — EPERM is proof of existence, not
// death, since a merge gate run under another account is still a live
// holder this sweep must not remove out from under.
func gatePRMergeHolderAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
