//go:build !windows

package postedit

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// detachedAttrs puts a spawned phase in its own session, so the hook's exit
// (and any signal aimed at its process group) leaves the build running.
func detachedAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// processStartTime asks ps for the OS's own creation timestamp of a live
// pid, so pidStillOurs can tell a live process from whatever the OS handed
// the same pid to after ours exited. /proc/<pid>/stat carries the same fact
// as a tick count since boot, which needs the boot time AND the kernel's
// clock-ticks-per-second to become a wall time; ps already does that
// arithmetic portably (Linux, macOS, BSD) without a new dependency. False
// means the pid names no process right now, or ps could not be run.
func processStartTime(pid int) (time.Time, bool) {
	out, err := run.LightOutput(run.Spec{Name: "ps", Args: []string{"-o", "lstart=", "-p", strconv.Itoa(pid)}, Env: append(os.Environ(), "LC_ALL=C")})
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
