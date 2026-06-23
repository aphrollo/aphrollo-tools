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
	c, p = filepath.Clean(c), filepath.Clean(p)
	return c == p || strings.HasPrefix(c, p+string(filepath.Separator))
}
