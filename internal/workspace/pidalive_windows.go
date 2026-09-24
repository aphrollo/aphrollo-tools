//go:build windows

package workspace

import "syscall"

// gatePRMergeHolderAlive reports whether a process with this pid exists.
// os.FindProcess always succeeds on Windows and Signal(0) is not implemented
// there, so the only portable answer is opening the process directly — the
// same shape internal/tdd/core's own pidRunning (mutants_windows_core.go)
// uses.
func gatePRMergeHolderAlive(pid int) bool {
	const (
		processQueryLimitedInformation = 0x1000
		stillActive                    = 259
		errorInvalidParameter          = syscall.Errno(87)
		errorAccessDenied              = syscall.Errno(5)
	)
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		if err == errorInvalidParameter {
			return false
		}
		if err == errorAccessDenied {
			return true
		}
		return true
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
