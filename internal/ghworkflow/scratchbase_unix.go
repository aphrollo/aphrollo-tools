//go:build !windows

package ghworkflow

import "os"

// shortScratchBase is the directory a run's scratch is made under: the OS temp
// dir, which off Windows is short and has no path limit to meet.
func shortScratchBase() string { return os.TempDir() }
