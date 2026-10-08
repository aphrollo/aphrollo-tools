package cli

import (
	"os"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/handoff"
	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// versionBinaryLine says which binary is running and how it relates to the
// user-space install hooks follow, so "which aphrollo is this" never needs
// guessing after an update.
func versionBinaryLine() string {
	return versionBinaryLineOnly() + newerInstallLine()
}

func versionBinaryLineOnly() string {
	exe := rawExecutablePath()
	root, err := userbin.Root()
	if err != nil {
		return "binary: " + exe + " (no user-space install dir on this account)\n"
	}
	cur, haveCur := userbin.Current(root)
	resolved := exe
	if r, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		resolved = r
	}
	switch {
	case userbin.Under(root, exe) || userbin.Under(root, resolved):
		if haveCur && (sameFilePath(exe, userbin.BinaryPath(root, cur)) || sameFilePath(resolved, userbin.BinaryPath(root, cur))) {
			return "binary: " + exe + " (the user-space current)\n"
		}
		return "binary: " + exe + " (user-space, not the current: current is v" + cur + ")\n"
	case haveCur:
		return "binary: " + exe + " (not the user-space install; the user-space current is v" + cur + ")\n"
	}
	return "binary: " + exe + " (not the user-space install; none installed)\n"
}

// sameFilePath compares two paths by the files they name, as the filesystem
// would, so case and short-name spellings agree.
func sameFilePath(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// newerInstallLine names a user-space install newer than this binary, or is
// empty: the line a stale root-owned install prints so it is not mistaken for
// the one the gate runs.
func newerInstallLine() string {
	root, err := userbin.Root()
	if err != nil {
		return ""
	}
	if v, p, ok := handoff.Newer(buildinfo.Version(), root); ok {
		return "newer install: " + v + " at " + p + "\n"
	}
	return ""
}
