package proc

import (
	"errors"
	"runtime"
	"syscall"
)

// IsResourceExhausted reports whether err says the OS could not give this
// process a new process or thread: EAGAIN/ENOMEM on unix, and on Windows
// ERROR_NOT_ENOUGH_MEMORY (8), ERROR_NO_SYSTEM_RESOURCES (1450) and
// ERROR_COMMITMENT_LIMIT (1455).
func IsResourceExhausted(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && exhausted(runtime.GOOS, uintptr(errno))
}

func exhausted(goos string, code uintptr) bool {
	if goos == "windows" {
		return code == 8 || code == 1450 || code == 1455
	}
	return code == uintptr(syscall.EAGAIN) || code == uintptr(syscall.ENOMEM)
}
