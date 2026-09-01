//go:build !windows

package tdd

import (
	"strconv"
	"syscall"
)

// detachedAttrs puts a spawned phase in its own session, so the hook's exit
// (and any signal aimed at its process group) leaves the build running.
func detachedAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// killTreePlan describes the kill for a test and a reader: a NEGATIVE pid is
// the process group, which is why detachedAttrs puts each phase in its own
// session — killing the wrapper alone left its cargo children running.
func killTreePlan(pid int) []string {
	return []string{"kill", "-KILL", "-" + strconv.Itoa(pid)}
}

// killTree signals the phase's whole process group.
func killTree(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
