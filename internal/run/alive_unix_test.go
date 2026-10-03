//go:build !windows

package run

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// alive reports whether the process pid is still running: a zombie is dead.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err != nil || !strings.Contains(string(stat), ") Z")
}

// treePidExpr is the pid of a bash child as the OS knows it.
const treePidExpr = `echo "$1"`

// bashCommand is bash; "" when the box has none.
func bashCommand() string {
	p, _ := exec.LookPath("bash")
	return p
}
