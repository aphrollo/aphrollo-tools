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
	"runtime/debug"
	"strings"
	"testing"
)

// commit, builtAt and version are set by the linker (see
// internal/cli/selfinstall.go's buildArgs),
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

// readBuildInfo is where the module build information is read from, a seam so a
// test can say what Go recorded.
var readBuildInfo = debug.ReadBuildInfo

// Module is what Go recorded of a binary built from a module version, as
// `go install module/cmd/x@v1.2.3` does.
type Module struct {
	Version  string // as Go records it, with the leading v
	Revision string // the first seven characters of vcs.revision, "" when none was recorded
	Modified bool   // vcs.modified: the tree the binary was built from had edits
}

// ModuleBuild reports the module version this binary was built from, when it is
// a release tag: a build made by `go install` at a tag has no linker stamp, and
// this is what says which release it is. It is none for a build from a checkout
// ((devel)), from a pseudo-version or from anything that is not MAJOR.MINOR.PATCH.
func ModuleBuild() (Module, bool) {
	info, ok := readBuildInfo()
	if !ok || info == nil || !releaseStamp.MatchString(strings.TrimPrefix(info.Main.Version, "v")) {
		return Module{}, false
	}
	m := Module{Version: info.Main.Version}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			m.Revision = s.Value[:min(len(s.Value), 7)]
		case "vcs.modified":
			m.Modified = s.Value == "true"
		}
	}
	return m, true
}

// Released reports whether this binary was built at a release tag, by the linker
// stamp or as a module at that tag.
func Released() bool { return releasedVersion() != "" }

// releasedVersion is the release the binary is, MAJOR.MINOR.PATCH, or "": the
// linker stamp first, else the module version a `go install` recorded.
func releasedVersion() string {
	if v := stampedVersion(); v != "" {
		return v
	}
	if m, ok := ModuleBuild(); ok {
		return strings.TrimPrefix(m.Version, "v")
	}
	return ""
}

// Version is the semantic version this binary was built at, MAJOR.MINOR.PATCH,
// or "0.0.0-dev+<sha>" (plain "0.0.0-dev" when no commit was stamped) for a
// build that is not at a release tag.
func Version() string {
	if v := releasedVersion(); v != "" {
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

// SetModuleBuildForTest makes this binary read as built from the module version
// v (with its leading v, or "(devel)") recorded at revision, modified or not, and
// returns the function that puts the real reading back. Like the other setters it
// panics outside a test binary.
func SetModuleBuildForTest(v, revision string, modified bool) (restore func()) {
	if !testing.Testing() {
		panic("buildinfo.SetModuleBuildForTest called outside a test")
	}
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		info := &debug.BuildInfo{Main: debug.Module{Version: v}}
		if revision != "" {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "vcs.revision", Value: revision})
		}
		if modified {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "vcs.modified", Value: "true"})
		}
		return info, true
	}
	return func() { readBuildInfo = prev }
}
