//go:build !windows

package gc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ownedByCurrentUser reports whether the sweeping user owns the entry: a
// directory of another account in a shared temp dir is not this sweep's to
// delete.
func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// dirHeldByProcess reports whether any live process has dir as its working
// directory, its executable, or an open file. known is false where procfs is
// absent, which raises the age bar instead of guessing.
func dirHeldByProcess(dir string) (held, known bool) {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return false, false
	}
	inside := func(target string) bool {
		target = strings.TrimSuffix(target, " (deleted)")
		return target == dir || strings.HasPrefix(target, dir+string(filepath.Separator))
	}
	for _, p := range procs {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue // not a process directory
		}
		base := filepath.Join("/proc", p.Name())
		for _, link := range []string{"cwd", "exe"} {
			if target, err := os.Readlink(filepath.Join(base, link)); err == nil && inside(target) {
				return true, true
			}
		}
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if err != nil {
			continue // another user's process: it cannot be holding ours
		}
		for _, fd := range fds {
			if target, err := os.Readlink(filepath.Join(base, "fd", fd.Name())); err == nil && inside(target) {
				return true, true
			}
		}
	}
	return false, true
}
