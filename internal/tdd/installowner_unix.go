//go:build !windows

package tdd

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// InstallOwner reports the username that owns path's underlying inode, ""
// when path cannot be stat'd, the platform's Sys() carries no uid, or the
// uid has no /etc/passwd entry to name it (the numeric uid itself is a
// worse answer than no answer at all — an operator can't act on "owned by
// 999" any better than on silence).
func InstallOwner(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	u, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10))
	if err != nil || u.Username == "" {
		return ""
	}
	return u.Username
}
