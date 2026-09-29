package depinstall

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// isLink reports whether an entry of this type is a link to be unlinked, never
// descended into or deleted through: a symlink, or what Go reports for a
// Windows directory junction — ModeIrregular with no ModeDir. A junction to
// the primary checkout's node_modules is the common case a lane carries.
func isLink(mode fs.FileMode) bool {
	return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

// RemoveLinks unlinks every symlink and directory junction under root, root
// included, without following any of them. It runs before anything that
// deletes root recursively: `git worktree remove` and an os.RemoveAll from an
// older Go on Windows can recurse through a junction into its target. Each
// link goes through os.Remove, which unlinks a link and refuses a real
// non-empty directory, so a link it cannot unlink is an error and the caller
// removes nothing. A path already gone needs nothing.
func RemoveLinks(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !isLink(entryType(d)) {
			return nil
		}
		return os.Remove(p)
	})
}

// RemoveTree deletes root and everything in it, unlinking every link first so
// the deletion never reaches a link's target.
func RemoveTree(root string) error {
	if err := RemoveLinks(root); err != nil {
		return err
	}
	return os.RemoveAll(root)
}

// junctions is the set of entry names TreatAsJunction makes RemoveLinks see
// as a junction.
var junctions struct {
	sync.Mutex
	names map[string]bool
}

// entryType is d's type, as Windows would report it for a name
// TreatAsJunction registered.
func entryType(d fs.DirEntry) fs.FileMode {
	junctions.Lock()
	fake := junctions.names[d.Name()]
	junctions.Unlock()
	if fake {
		return fs.ModeIrregular
	}
	return d.Type()
}

// TreatAsJunction makes RemoveLinks see every entry with one of these base
// names as a Windows directory junction and returns the restore. A test seam:
// a junction exists only on Windows, and this runs that branch anywhere.
func TreatAsJunction(names ...string) (restore func()) {
	junctions.Lock()
	defer junctions.Unlock()
	prev := junctions.names
	next := map[string]bool{}
	for _, n := range names {
		next[n] = true
	}
	junctions.names = next
	return func() {
		junctions.Lock()
		junctions.names = prev
		junctions.Unlock()
	}
}
