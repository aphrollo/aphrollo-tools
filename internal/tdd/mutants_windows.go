//go:build windows

package tdd

import (
	"syscall"
)

// pidRunning reports whether a process with this pid exists. os.FindProcess
// always succeeds on Windows and Signal(0) is not implemented there, so the
// only portable answer is the task list, filtered by the OS rather than
// parsed by us.
func pidRunning(pid int) bool {
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
		// A pid that never existed, or one whose process has exited and been
		// reaped, is gone — the one answer this function exists to give.
		if err == errorInvalidParameter {
			return false
		}
		// Anything else (a process this token may not open, most of all) is
		// "cannot tell". A job reported running that is not costs a session
		// one look; a job reported finished that is not costs a duplicate run.
		if err == errorAccessDenied {
			return true
		}
		return true
	}
	// Nothing to do about a failed close, and nothing to report: the answer
	// below is already decided by then.
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	// A process that has exited still has an openable handle until the last
	// one is closed, so the handle alone does not mean alive: the exit code
	// does. STILL_ACTIVE is what a running process reports.
	return code == stillActive
}
