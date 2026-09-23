package core

import (
	"path/filepath"
	"runtime"
	"strings"
)

// sameProject matches a gate-log root against the root the badge is rendering
// for. A stage logs the root it RAN in, which for a cargo workspace member is
// a directory inside the project, so a nested root counts as this project.
func sameProject(logged, root string) bool {
	// Both sides go through logToken: that is the form the log carries, and a
	// path with a space in it has to compare equal to its own logged spelling.
	logged, root = filepath.Clean(LogToken(logged)), filepath.Clean(LogToken(root))
	if runtime.GOOS == "windows" {
		logged, root = strings.ToLower(logged), strings.ToLower(root)
	}
	if logged == root {
		return true
	}
	return strings.HasPrefix(logged, root+string(filepath.Separator))
}
