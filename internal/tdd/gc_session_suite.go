package tdd

import (
	"os"
	"path/filepath"
)

// gcStatePath resolves a state-dir file, creating the dir. "" when there is
// no state dir at all (then nothing about the sweep is remembered, which is
// the same as it never having run).
func gcStatePath(name string) string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return filepath.Join(dir, name)
}
