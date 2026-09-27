//go:build windows

package core

import (
	"strconv"

	"golang.org/x/sys/windows"
)

// processIdentity names the process holding pid by its creation time, which
// a pid reused after a reboot or after its process exits does not share. ok
// is false when the process cannot be opened or its times cannot be read.
func processIdentity(pid int) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", false // absence-ok: no process this token can open holds pid, so nothing proves who holds it
	}
	// Nothing to do about a failed close: the answer is already read.
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", false // absence-ok: a process whose creation time cannot be read cannot be told from a pid reuse
	}
	return strconv.FormatInt(created.Nanoseconds(), 10), true
}
