package pkg

import "path/filepath"

// matchesGlob compares a name against a glob with filepath.Match, whose
// separator handling differs by OS.
func matchesGlob(pattern, name string) bool {
	ok, _ := filepath.Match(pattern, name)
	return ok
}
