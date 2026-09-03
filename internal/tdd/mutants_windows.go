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
// DETACHED_PROCESS is the load-bearing one, and it is what the deferred phase
// deliberately does NOT use: a mutation run started from an agent's tool call
// died twice at mutant 101 of 131 because the shell's own timeout took the
// whole console process tree with it (issue #103). A job that shares a console
// with the shell that started it is a job that shell can kill. The cost is the
// one CREATE_NO_WINDOW exists to avoid — a console app started by the detached
// process may allocate its own console — which is why every stream is
// redirected to a FILE at the spawn site: nothing in the tree has a reason to
// write to a console at all.
//
// The priority is the second point: a mutation run is hours of compiling
// nobody is waiting on, and at normal priority it competes for the same cores
// as the edit-time suite a session IS waiting on.
func belowNormalAttrs() *syscall.SysProcAttr {
	const (
		createNewProcessGroup   = 0x00000200
		detachedProcess         = 0x00000008
		belowNormalPriorityFlag = 0x00004000
	)
	return &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess | belowNormalPriorityFlag,
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
