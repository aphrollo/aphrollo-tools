package pkg

import "runtime"

// binGOOS is read once behind a package variable a test can override, the
// shape internal/cli/selfinstall.go carries in the real tree — both branches
// of every OS check built on it are reachable from either host.
var binGOOS = runtime.GOOS

func isWindowsSeamed() bool {
	return binGOOS == "windows"
}
