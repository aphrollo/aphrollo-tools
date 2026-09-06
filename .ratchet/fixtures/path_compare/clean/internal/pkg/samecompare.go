package pkg

import (
	"path/filepath"
	"strings"
)

// samePath is the model this law points at: it normalizes both sides
// before comparing, so no raw path-named == or HasPrefix(path, ...) appears
// on the way to an answer.
func samePath(a, b string) bool {
	clean := func(p string) string {
		return strings.ToLower(filepath.Clean(p))
	}
	return clean(a) == clean(b)
}

// insideDir is the other model: base and path are cleaned first, and the
// prefix question is answered by filepath.Rel rather than a raw HasPrefix.
func insideDir(base, path string) bool {
	base, path = filepath.Clean(base), filepath.Clean(path)
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
