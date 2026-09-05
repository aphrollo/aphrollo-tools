// Package buildinfo holds the commit and build time the linker stamps into
// the binary via -ldflags -X. It imports nothing from this module: later
// packages (tdd's update verb, a session-start behind-notice) need to read
// the stamp without pulling in the rest of the CLI.
package buildinfo

import "testing"

// commit and builtAt are set by the linker (see internal/cli/selfinstall.go's
// buildArgs), never assigned at runtime outside SetForTest.
var (
	commit  string
	builtAt string
)

// Stamp reports what the linker set. stamped is false when the binary was
// built without the -ldflags stamp (e.g. `go build` run by hand), so a
// caller can say "unstamped" instead of printing a misleadingly empty commit.
// Unnamed returns on purpose: named returns of the same spelling as the
// package vars above would shadow them inside this function's body.
func Stamp() (string, string, bool) {
	if commit == "" {
		return "", "", false
	}
	return commit, builtAt, true
}

// SetForTest sets the package vars for a test that needs a stamped binary
// without an actual linker run. It panics outside a test binary so it can
// never be reached from production code.
func SetForTest(c, b string) {
	if !testing.Testing() {
		panic("buildinfo.SetForTest called outside a test")
	}
	commit = c
	builtAt = b
}
