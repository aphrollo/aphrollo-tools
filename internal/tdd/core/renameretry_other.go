//go:build !windows

package core

// renameRetryable is false off Windows: a rename over a file another process
// has open succeeds there, so a failure is not one waiting clears.
func renameRetryable(error) bool { return false }
