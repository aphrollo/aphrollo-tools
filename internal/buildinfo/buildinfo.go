// Package buildinfo holds what the linker stamps into the binary via -ldflags
// -X: the commit and build time, and the release version. It imports nothing
// from this module: later packages (tdd's update verb, a session-start
// behind-notice) need to read the stamp without pulling in the rest of the CLI.
//
// The version comes from the release tag a build is made at, never from a
// file in the source: a PR does not carry the number, so two lanes never
// collide on it. A build that is not at a release tag is a dev build.
package buildinfo

import (
	"regexp"
	"strings"
	"testing"
)

// commit, builtAt and version are set by the linker (see
// internal/cli/selfinstall.go's buildArgs and .github/workflows/deploy.yml),
// never assigned at runtime outside the test setters.
var (
	commit  string
	builtAt string
	version string
)

// devVersion is what a build that is not at a release tag reports, before the
// commit it was built at. It is a semantic-version pre-release of 0.0.0, so it
// sorts below every release.
const devVersion = "0.0.0-dev"

var releaseStamp = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$`)

// stampedVersion is the linker's version stamp as MAJOR.MINOR.PATCH, or "" when
// it set none or set something that is not a release version. A tag's leading
// v is dropped.
func stampedVersion() string {
	v := strings.TrimPrefix(version, "v")
	if !releaseStamp.MatchString(v) {
		return ""
	}
	return v
}

// Released reports whether this binary was built at a release tag.
func Released() bool { return stampedVersion() != "" }

// Version is the semantic version this binary was built at, MAJOR.MINOR.PATCH,
// or "0.0.0-dev+<sha>" (plain "0.0.0-dev" when no commit was stamped) for a
// build that is not at a release tag.
func Version() string {
	if v := stampedVersion(); v != "" {
		return v
	}
	if short := commit; short != "" {
		return devVersion + "+" + short[:min(len(short), 7)]
	}
	return devVersion
}

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

// SetVersionForTest sets the version stamp for a test that needs a build at a
// release tag. Like SetForTest it panics outside a test binary.
func SetVersionForTest(v string) {
	if !testing.Testing() {
		panic("buildinfo.SetVersionForTest called outside a test")
	}
	version = v
}
