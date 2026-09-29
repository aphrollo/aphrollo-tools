//go:build windows

package gc

import "os"

// ownedByCurrentUser: the per-user temp dir on Windows is the user's own.
func ownedByCurrentUser(os.FileInfo) bool { return true }

// dirHeldByProcess cannot ask the OS who holds a directory, so the sweep
// falls back to the day-long age bar.
func dirHeldByProcess(string) (held, known bool) { return false, false }
