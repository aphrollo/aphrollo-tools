//go:build windows

package tdd

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// belowNormalAttrs starts the mutation job outside every console this box
// has, in its own process group, at BELOW NORMAL priority.
//
// Not sharing the starting shell's console is the load-bearing part: a
// mutation run started from an agent's tool call died twice at mutant 101 of
// 131 because the shell's own timeout took the whole console process tree with
// it (issue #103). A job that shares a console with the shell that started it
// is a job that shell can kill.
//
// CREATE_NO_WINDOW buys that without DETACHED_PROCESS's cost. It gives the job
// its OWN console — so the starting shell's console tree does not contain it,
// which is all #103 needed — and that console is never shown and is INHERITED
// by every child. DETACHED_PROCESS instead leaves the job with no console at
// all, so the first console program below it allocates a fresh one and Windows
// shows it: one window per Go child, for the hours a mutation run lasts.
// Redirecting every stream to a FILE does not prevent that, because a console
// program allocates a console whether or not it writes to it.
//
// This is the same fix the deferred phase already carries in detachedAttrs;
// this spawn path was simply left behind when that one was corrected.
//
// The priority is the second point: a mutation run is hours of compiling
// nobody is waiting on, and at normal priority it competes for the same cores
// as the edit-time suite a session IS waiting on.
func belowNormalAttrs() *syscall.SysProcAttr {
	const (
		createNewProcessGroup   = 0x00000200
		createNoWindow          = 0x08000000
		belowNormalPriorityFlag = 0x00004000
	)
	return &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow | belowNormalPriorityFlag,
	}
}

// lowerOwnPriority is a no-op here: the creation flag above already put this
// process — and therefore every compiler it starts — below normal.
func lowerOwnPriority() {}

// pidRunning reports whether a process with this pid exists. os.FindProcess
// always succeeds on Windows and Signal(0) is not implemented there, so the
// only portable answer is the task list, filtered by the OS rather than
// parsed by us.
func pidRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		// Cannot tell. A job reported running that is not costs a session one
		// look; a job reported finished that is not costs a duplicate run.
		return true
	}
	return strings.Contains(string(out), "\""+strconv.Itoa(pid)+"\"")
}
