package tdd

import (
	"os"
	"path/filepath"
)

// rustFileIsTestModule reports whether a #[cfg(test)] `#[path]` declaration
// in the crate mounts file.
func rustFileIsTestModule(root, rel, file string) bool {
	srcDir, ok := cargoSrcDirOf(rel)
	if !ok {
		return false
	}
	m, ok := scanPathMounts(filepath.Join(root, filepath.FromSlash(srcDir)))[mountKey(file)]
	if !ok {
		return false
	}
	data, err := os.ReadFile(m.decl)
	if err != nil {
		return false
	}
	return rustMountsAsTest(m.decl, string(data), file)
}
