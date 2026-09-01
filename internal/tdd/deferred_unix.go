//go:build !windows

package tdd

import "syscall"

// detachedAttrs puts a spawned phase in its own session, so the hook's exit
// (and any signal aimed at its process group) leaves the build running.
func detachedAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
