package cli

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

// newestReleaseTag picks the highest v<MAJOR.MINOR.PATCH> tag. A tag that is
// not exactly that form (a salvage ref, a pre-release, a bare number) is not
// a release and is never chosen. ok is false when no tag is a release.
func newestReleaseTag(tags []string) (string, bool) {
	var best compat.Version
	var found string
	for _, tag := range tags {
		name, isRelease := strings.CutPrefix(tag, "v")
		if !isRelease {
			continue
		}
		v, err := compat.ParseVersion(name)
		if err != nil {
			continue
		}
		if found == "" || best.Less(v) {
			best, found = v, tag
		}
	}
	return found, found != ""
}

// tagOlderThanRunning reports whether a release tag names an older version
// than the running binary, so `update` never downgrades a box to an old tag
// the remote still carries. A tag that is not a release is not older.
func tagOlderThanRunning(tag string) bool {
	v, err := compat.ParseVersion(strings.TrimPrefix(tag, "v"))
	return err == nil && v.Less(compat.Binary())
}
