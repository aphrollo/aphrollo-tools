//go:build !windows

package core

import "os"

// openShared is os.Open off Windows: a rename over a file another process has
// open succeeds there, and the open handle goes on reading the old content.
func openShared(path string) (*os.File, error) { return os.Open(path) }
