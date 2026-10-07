package cli

import (
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// versionBinaryLine says which binary is running and how it relates to the
// user-space install hooks follow, so "which aphrollo is this" never needs
// guessing after an update.
func versionBinaryLine() string {
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

// sameFilePath compares two paths as the filesystem would, by the files they name when both exist.
func sameFilePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	return aliasResolvesTo(a, b)
}
