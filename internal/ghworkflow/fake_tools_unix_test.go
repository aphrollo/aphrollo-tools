//go:build !windows

// twin: internal/ghworkflow/fake_tools_windows_test.go
package ghworkflow

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// probePriority says whether this process runs below normal priority: its
// nice value, as ps reports it, is above zero.
func probePriority() string {
	out, err := exec.Command("ps", "-o", "ni=", "-p", strconv.Itoa(os.Getpid())).Output() // stderr-ok: a failed probe is reported as unknown below
	if err != nil {
		return "unknown: " + err.Error()
	}
	if nice, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && nice > 0 {
		return "low"
	}
	return "normal"
}
