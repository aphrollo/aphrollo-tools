package release

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

// FrozenThrough is the last release whose notes live in CHANGELOG.md: the
// newest version section the file carries. Every release after it is written as
// the fragments its tag first contains. Reading it from the file rather than
// writing a number into the code means the last release written by hand is the
// cutoff whenever it landed. ok is false for a changelog with no sections.
func FrozenThrough(changelog string) (v compat.Version, ok bool) {
	for _, s := range Sections(changelog) {
		if sv, err := compat.ParseVersion(s.Version); err == nil && (!ok || v.Less(sv)) {
			v, ok = sv, true
		}
	}
	return v, ok
}

// ReleaseTag is a tag that names a release, v<MAJOR.MINOR.PATCH>.
type ReleaseTag struct {
	Tag     string
	Version compat.Version
}

// compareVersions orders two versions: negative when a is older.
func compareVersions(a, b compat.Version) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

// releaseTags keeps the tags that are exactly v<MAJOR.MINOR.PATCH>, oldest
// first: a salvage ref, a pre-release or a bare number is no release.
func releaseTags(tags []string) []ReleaseTag {
	var out []ReleaseTag
	for _, tag := range tags {
		name, isRelease := strings.CutPrefix(tag, "v")
		if !isRelease {
			continue
		}
		v, err := compat.ParseVersion(name)
		if err != nil {
			continue
		}
		out = append(out, ReleaseTag{Tag: tag, Version: v})
	}
	slices.SortFunc(out, func(a, b ReleaseTag) int { return compareVersions(a.Version, b.Version) })
	return out
}

// NewestRelease is the highest release tag among tags.
func NewestRelease(tags []string) (tag string, v compat.Version, ok bool) {
	all := releaseTags(tags)
	if len(all) == 0 {
		return "", compat.Version{}, false
	}
	newest := all[len(all)-1]
	return newest.Tag, newest.Version, true
}

// LaterReleaseTags are the release tags after the frozen version, oldest
// first: the ones whose notes are fragments.
func LaterReleaseTags(tags []string, frozen compat.Version) []ReleaseTag {
	var later []ReleaseTag
	for _, r := range releaseTags(tags) {
		if frozen.Less(r.Version) {
			later = append(later, r)
		}
	}
	return later
}

// NextVersion is the version after newest for a release of the given level:
// the number the level names goes up and every number below it resets.
func NextVersion(newest compat.Version, level compat.Bump) (compat.Version, error) {
	switch level {
	case compat.BumpMajor:
		return compat.Version{Major: newest.Major + 1}, nil
	case compat.BumpMinor:
		return compat.Version{Major: newest.Major, Minor: newest.Minor + 1}, nil
	case compat.BumpPatch:
		return compat.Version{Major: newest.Major, Minor: newest.Minor, Patch: newest.Patch + 1}, nil
	}
	return compat.Version{}, fmt.Errorf("a release is patch, minor or major, not %q", level)
}

// HighestLevel is the biggest level among fragments, none for no fragments.
func HighestLevel(fragments []Fragment) compat.Bump {
	best := compat.BumpNone
	for _, f := range fragments {
		if levelRank(f.Level) > levelRank(best) {
			best = f.Level
		}
	}
	return best
}

// Plan is the release a push to main owes: the tag to make and why.
type Plan struct {
	Tag       string
	Version   compat.Version
	Level     compat.Bump
	Fragments []Fragment
}

// PlanRelease decides whether a commit owes a release tag. tags are the
// repository's tags, inNewest the names of the fragments the newest release tag
// already contains, head the fragments at the commit to tag. The fragments to
// release are those at head the newest tag does not contain, and the next
// version is the newest bumped by the highest level among them. With none of
// level patch, minor or major there is nothing to release, which is also what a
// second run for the same push finds once the tag exists: the plan is relative
// to the newest tag, so it can never mint the same tag twice.
func PlanRelease(tags, inNewest []string, head []Fragment) (Plan, bool, error) {
	_, newest, ok := NewestRelease(tags)
	if !ok {
		return Plan{}, false, fmt.Errorf("no release tag (v<MAJOR.MINOR.PATCH>) to bump from; fetch the tags, or tag the first release by hand")
	}
	var pending []Fragment
	for _, f := range head {
		if f.Level != compat.BumpNone && !slices.Contains(inNewest, f.Name) {
			pending = append(pending, f)
		}
	}
	if len(pending) == 0 {
		return Plan{}, false, nil
	}
	slices.SortFunc(pending, func(a, b Fragment) int { return strings.Compare(a.Name, b.Name) })
	level := HighestLevel(pending)
	next, err := NextVersion(newest, level)
	if err != nil {
		return Plan{}, false, err
	}
	return Plan{Tag: "v" + next.String(), Version: next, Level: level, Fragments: pending}, true, nil
}

// TagTree is a release tag and the names of the fragments its tree holds.
type TagTree struct {
	Tag       string
	Version   compat.Version
	Fragments []string
}

// Release is a tag and the fragments it was the first to contain.
type Release struct {
	Tag       string
	Version   compat.Version
	Fragments []string
}

// Attribute assigns every fragment to the release that first contained it: the
// oldest tag whose tree holds it. What a tag's own tree holds is its history,
// even for a fragment later removed from the tree; a fragment no tag holds is
// unreleased when head has it. Releases come oldest first, names sorted; a tag
// that introduced no fragment is not a release.
func Attribute(trees []TagTree, head []string) (released []Release, unreleased []string) {
	ordered := slices.Clone(trees)
	slices.SortFunc(ordered, func(a, b TagTree) int { return compareVersions(a.Version, b.Version) })
	seen := map[string]bool{}
	for _, tree := range ordered {
		var fresh []string
		for _, name := range tree.Fragments {
			if !seen[name] {
				seen[name] = true
				fresh = append(fresh, name)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		slices.Sort(fresh)
		released = append(released, Release{Tag: tree.Tag, Version: tree.Version, Fragments: fresh})
	}
	for _, name := range head {
		if !seen[name] {
			unreleased = append(unreleased, name)
		}
	}
	slices.Sort(unreleased)
	return released, unreleased
}
