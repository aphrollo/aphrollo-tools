//go:build !windows

package workspace

import "os"

// repointSymlink atomically replaces symlink with one pointing at target, via
// a temp link + rename so a concurrent reader never sees a missing link.
// POSIX rename(2) replaces the link entry itself and never follows into what
// either side points at, so this holds even when the existing link points at
// a real, existing directory.
func repointSymlink(symlink, target string) error {
	tmp := symlink + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, symlink); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
