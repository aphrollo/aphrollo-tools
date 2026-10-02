//go:build !windows

package ghworkflow

// gitForWindowsBash has nothing to find off Windows: bash is on PATH.
func gitForWindowsBash() (string, bool) { return "", false }

// isWSLLauncher is never true off Windows.
func isWSLLauncher(string) bool { return false }
