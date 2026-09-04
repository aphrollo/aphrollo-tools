//go:build !windows

package tdd

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// processStartTime asks ps for the OS's own creation timestamp of a live
// pid, so pidStillOurs can tell a live process from whatever the OS handed
// the same pid to after ours exited. /proc/<pid>/stat carries the same fact
// as a tick count since boot, which needs the boot time AND the kernel's
// clock-ticks-per-second to become a wall time; ps already does that
// arithmetic portably (Linux, macOS, BSD) without a new dependency. False
// means the pid names no process right now, or ps could not be run.
func processStartTime(pid int) (time.Time, bool) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", s, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// suiteAttrs puts a SUITE child in its own process group, which is what makes
// killTree's negative-pid signal reach the launcher's children rather than
// only the launcher. It is deliberately NOT Setsid: the suite is a foreground
// child whose output the gate reads, not a detached phase.
func suiteAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
