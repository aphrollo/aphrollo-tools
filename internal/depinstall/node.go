package depinstall

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// NodeModules is the directory every node install rule produces.
const NodeModules = "node_modules"

// IsNode reports whether r installs a node_modules tree.
func (r Rule) IsNode() bool { return r.Present == NodeModules }

// Reusable reports whether laneRoot's installed node_modules serves root
// unchanged: r pins its dependencies in a lockfile, that lockfile's bytes are
// identical in both roots, and laneRoot has a node_modules to share. A bare
// package.json pins nothing, so it never qualifies.
func Reusable(r Rule, laneRoot, root string) bool {
	if !r.IsNode() || r.Marker == "package.json" {
		return false
	}
	fi, err := os.Stat(filepath.Join(laneRoot, NodeModules))
	return err == nil && fi.IsDir() && sameBytes(filepath.Join(root, r.Marker), filepath.Join(laneRoot, r.Marker))
}

// sameBytes reports whether both files exist and hold identical bytes.
func sameBytes(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

// linkGOOS picks the link kind; a seam so the Windows branch is testable on
// any platform.
var linkGOOS = runtime.GOOS

// junctionFn makes a directory junction at link pointing at target.
var junctionFn = makeJunction

// LinkDir makes link point at the directory target: a symlink on Unix, a
// directory junction on Windows, where a symlink needs a privilege a normal
// account lacks and a junction does not.
func LinkDir(target, link string) error {
	if linkGOOS == "windows" {
		return junctionFn(target, link)
	}
	return os.Symlink(target, link)
}

// junctionCmdLine is the exact cmd.exe command line that makes the junction.
// /s makes cmd strip only the outer quote pair, so each path keeps its own
// quotes; a Windows path cannot itself contain a quote.
func junctionCmdLine(link, target string) string {
	return fmt.Sprintf(`cmd.exe /s /c "mklink /J "%s" "%s""`, link, target)
}

// Links records the links made into a throwaway checkout so they can be
// removed as links before the checkout itself is deleted. Safe for the
// signal handler and the normal return path to share.
type Links struct {
	mu    sync.Mutex
	paths []string
}

// Make links link to target and records it.
func (l *Links) Make(target, link string) error {
	if err := LinkDir(target, link); err != nil {
		return err
	}
	l.mu.Lock()
	l.paths = append(l.paths, link)
	l.mu.Unlock()
	return nil
}

// Remove deletes every recorded link itself, never its target: os.Remove
// unlinks a symlink or junction and refuses a non-empty directory, so it can
// never recurse into the lane's real node_modules. Each link is removed once;
// one already gone needs nothing.
func (l *Links) Remove() {
	l.mu.Lock()
	paths := l.paths
	l.paths = nil
	l.mu.Unlock()
	for _, p := range paths {
		_ = os.Remove(p)
	}
}
