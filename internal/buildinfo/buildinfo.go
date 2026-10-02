// Package buildinfo holds the commit and build time the linker stamps into
// the binary via -ldflags -X, and the semantic version the source carries. It
// imports nothing from this module: later packages (tdd's update verb, a
// session-start behind-notice) need to read the stamp without pulling in the
// rest of the CLI.
package buildinfo

import (
	_ "embed"
	"strings"
	"testing"
)

// versionFile is the one place the semantic version lives: VERSION, beside
// this file, compiled into every build. Its bump rule and its changelog
// section are enforced by `aphrollo version check` (internal/compat).
//
//go:embed VERSION
var versionFile string

// Version is the semantic version this binary was built at, MAJOR.MINOR.PATCH.
// Unlike the stamp it is present in a build made by hand with no linker flags.
func Version() string {
	return strings.TrimSpace(versionFile)
}

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
