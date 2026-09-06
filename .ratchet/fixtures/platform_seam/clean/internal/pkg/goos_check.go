package pkg

import "runtime"

// isWindows reads runtime.GOOS directly, admitted by the escape comment
// below because nothing here is predicted or seam-worthy — a fixture pin,
// not real platform logic.
func isWindows() bool {
	// goos-ok: fixture pin for the platform_seam law itself
	if runtime.GOOS == "windows" {
		return true
	}
	return false
}
