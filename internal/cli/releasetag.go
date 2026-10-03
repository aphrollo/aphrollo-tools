package cli

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/release"
)

// newestReleaseTag picks the highest v<MAJOR.MINOR.PATCH> tag. A tag that is
// not exactly that form (a salvage ref, a pre-release, a bare number) is not
// a release and is never chosen. ok is false when no tag is a release.
func newestReleaseTag(tags []string) (string, bool) {
	tag, _, ok := release.NewestRelease(tags)
	return tag, ok
}

// tagOlderThanRunning reports whether a release tag names an older version
// than the running binary, so `update` never downgrades a box to an old tag
// the remote still carries. A tag that is not a release is not older, and a
// dev build, which has no version, is older than no tag.
func tagOlderThanRunning(tag string) bool {
	running := compat.Binary()
	if running.Dev {
		return false
	}
	v, err := compat.ParseVersion(strings.TrimPrefix(tag, "v"))
	return err == nil && v.Less(running.Version)
}
