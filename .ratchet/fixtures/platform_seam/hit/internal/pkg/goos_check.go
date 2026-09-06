package pkg

import "runtime"

// isWindows reads runtime.GOOS directly, with no seam a test can override to
// exercise the other branch on either host.
func isWindows() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return false
}
