//go:build !windows

package handoff

import (
	"os"
	"syscall"
)

// Launch replaces this process with path: same argv, stdin, env, and the exit
// code is the new program's own. It returns only when the exec failed.
func Launch(path string, args, env []string, announce func()) (int, error) {
	announce()
	return 0, syscall.Exec(path, append([]string{path}, args...), env)
}

// OwnedByUser reports whether path belongs to the running account.
func OwnedByUser(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}
