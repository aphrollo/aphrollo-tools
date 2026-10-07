package cli

import (
	"path/filepath"
	"slices"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// launcherDir is the directory PATH carries for bin: for a user-space binary
// the root, whose launcher follows `current`, so a version switch reaches
// every open shell without touching PATH; for any other binary its own dir.
func launcherDir(bin string) string {
	if root, err := userbin.Root(); err == nil && userbin.Under(root, bin) {
		return root
	}
	return filepath.Dir(bin)
}

// staleUserBinDir reports whether dir lies in the user-space install's tree
// (the root's parent) and is none of keep: a version directory, or the queue
// shim dir inside one, that an earlier install put on a PATH. The next prune
// deletes it, and until then it runs an old version ahead of the launcher.
func staleUserBinDir(dir string, keep ...string) bool {
	root, err := userbin.Root()
	if err != nil || dir == "" {
		return false
	}
	if tree := filepath.Dir(root); !userbin.Under(tree, dir) {
		return false
	}
	return !slices.ContainsFunc(keep, func(k string) bool { return samePathDir(k, dir) })
}

// samePathDir reports whether a and b name one directory, the way the
// platform compares paths (case-blind on Windows).
func samePathDir(a, b string) bool {
	rel, err := filepath.Rel(filepath.Clean(a), filepath.Clean(b))
	return err == nil && rel == "."
}
