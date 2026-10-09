//go:build !windows

package depinstall

import "path/filepath"

// realPath is where path really is, symlinks followed.
func realPath(path string) (string, error) { return filepath.EvalSymlinks(path) }
