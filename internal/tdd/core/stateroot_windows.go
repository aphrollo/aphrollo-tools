//go:build windows

package core

import "os"

// localAppData is the per-user local data directory Windows sets.
func localAppData() string { return os.Getenv("LOCALAPPDATA") }
