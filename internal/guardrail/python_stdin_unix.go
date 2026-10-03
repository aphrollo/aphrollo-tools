//go:build !windows

package guardrail

// nullStdinIsTTY is false off Windows: /dev/null is not a terminal there, so
// `python -` on it reads end of file and exits.
var nullStdinIsTTY = false
