//go:build windows

package guardrail

// nullStdinIsTTY is true on Windows: Git Bash maps /dev/null to NUL, a
// character device that python's isatty() reports as a terminal.
var nullStdinIsTTY = true
