//go:build !windows

package core

import "os"

// renameRetryable is false off Windows: a rename over a file another process
// has open succeeds there, so a failure is not one waiting clears.
func renameRetryable(error) bool { return false }

// replaceFile moves tmp over dst: a plain rename off Windows, which replaces
// atomically whatever readers hold dst open.
func replaceFile(tmp, dst string) error { return os.Rename(tmp, dst) }
