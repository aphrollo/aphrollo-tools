//go:build windows

package tdd

import "syscall"

// waitForPIDExit blocks this process on pid's own process handle via
// Windows' WaitForSingleObject — the OS telling this process when pid ends,
// never this process sampling pidRunning (or any file) on an interval. This
// is the primitive `status --wait` needed and did not have: every session
// that wanted the same thing wrote a shell loop sleeping 30s at a time over
// the receipt file instead (issue #272).
//
// A pid this process cannot even open (already gone, recycled, or never
// existed) returns at once: there is nothing left to wait for, and the
// caller's own re-read of the status decides what that means.
func waitForPIDExit(pid int) {
	if pid <= 0 {
		return
	}
	const synchronize = 0x00100000
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	const infinite = 0xFFFFFFFF
	_, _ = syscall.WaitForSingleObject(h, infinite)
}
