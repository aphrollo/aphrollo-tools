package lawgate

import (
	"os"
	"path/filepath"
)

// existingAncestorDir walks up from dir to the first directory that exists, ""
// when none does. A Write creates its parents, so the directory a hook has to
// ask git about routinely does not exist yet — and every git question asked
// from a missing directory fails, which reads as "no repo" and lets the write
// through.
func existingAncestorDir(dir string) string {
	for dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// repoRootNear resolves the repo containing dir, walking up past directories
// a write would still have to create. RepoRoot itself stays exact: a caller
// asking about a path on disk must not be answered about its grandparent.
func repoRootNear(dir string) string {
	return RepoRoot(existingAncestorDir(dir))
}
