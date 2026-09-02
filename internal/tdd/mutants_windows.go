//go:build windows

package tdd

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// belowNormalAttrs starts the mutation job detached, invisible and at BELOW
// NORMAL priority. The priority is the point: a mutation run is hours of
// compiling nobody is waiting on, and at normal priority it competes for the
// same cores as the edit-time suite a session IS waiting on.
//
// CREATE_NO_WINDOW rather than DETACHED_PROCESS for the same reason the
// deferred phase uses it: a detached child has no console, so the first
// compiler it starts makes Windows allocate and SHOW a new one.
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
