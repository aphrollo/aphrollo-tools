package suite

import (
	"path/filepath"
	"runtime"
	"strings"
)

// insideDir reports whether path lies lexically within base (inclusive).
// Windows compares case-insensitively — the same checkout routinely appears
// with both drive-letter casings.
func insideDir(base, path string) bool {
	base, path = filepath.Clean(base), filepath.Clean(path)
	if runtime.GOOS == "windows" {
		base, path = strings.ToLower(base), strings.ToLower(path)
	}
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
