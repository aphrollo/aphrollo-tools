//go:build !windows

package runtest

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Alive reports whether the process pid is still running: a zombie is dead.
func Alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err != nil || !strings.Contains(string(stat), ") Z")
}

// TreePidExpr is the pid of a bash child as the OS knows it.
const TreePidExpr = `echo "$1"`

// BashCommand is bash; "" when the box has none.
func BashCommand() string {
	p, _ := exec.LookPath("bash")
	return p
}
