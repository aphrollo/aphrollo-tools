package workspace

import (
	"path/filepath"
	"strings"
)

// pathWithin reports whether child is parent or a descendant of it, comparing
// cleaned absolute paths. The prune sweep uses it to recognise — and refuse to
// remove — the worktree the caller is standing in.
func pathWithin(child, parent string) bool {
	c, err1 := filepath.Abs(child)
	p, err2 := filepath.Abs(parent)
	if err1 != nil || err2 != nil {
		return false
	}
	// Resolve symlinks so a worktree reached through a symlinked cwd still matches
	// the canonical worktree path — the cwd guard is the only thing keeping prune
	// from removing the tree you stand in. Fall back to the Abs path when a path
	// can't be resolved (e.g. it doesn't exist yet) rather than crashing.
	if r, err := filepath.EvalSymlinks(c); err == nil {
		c = r
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	c, p = filepath.Clean(c), filepath.Clean(p)
	return c == p || strings.HasPrefix(c, p+string(filepath.Separator))
}
