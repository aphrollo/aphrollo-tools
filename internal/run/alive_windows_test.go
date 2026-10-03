//go:build windows

package run

import (
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// alive reports whether the process pid is still running.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }() // a probe handle; nothing to report
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// treePidExpr turns the MSYS pid of a bash child into the Windows pid.
const treePidExpr = `cat "/proc/$1/winpid"`

// bashCommand is an MSYS bash, the shell whose grandchildren survive taskkill
// /T; "" when the box has none. The System32 bash is WSL's, not this one.
func bashCommand() string {
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		if p := filepath.Join(pf, "Git", "bin", "bash.exe"); fileExists(p) {
			return p
		}
	}
	p, _ := exec.LookPath("bash")
	return p
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
