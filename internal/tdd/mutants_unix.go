//go:build !windows

package tdd

import (
	"os"
	"syscall"
)

// belowNormalAttrs puts the mutation job in its own session so the hook's exit
// leaves it running. The priority drop happens in the job itself
// (lowerOwnPriority), because a nice value set here would not survive exec.
func belowNormalAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// lowerOwnPriority drops this process — and every compiler it goes on to
// start, since niceness is inherited — below normal. A mutation run is hours
// of compiling nobody waits on; the edit-time suite a session IS waiting on
// must win every core they contend for.
func lowerOwnPriority() {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
}

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
