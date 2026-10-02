// Package compat is the compatibility contract between a binary and the
// repos it judges: the semantic version a build carries, the oldest version a
// repo declares it accepts (`requires` in aphrollo.toml), the rule that a
// change to what a consumer's gate says is a version bump, and the changelog
// section each version owes its consumers.
package compat

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// Version is MAJOR.MINOR.PATCH, nothing else: no pre-release tag, no build
// metadata, no leading v. The one shape keeps every compare a number compare.
type Version struct {
	Major, Minor, Patch int
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ParseVersion reads MAJOR.MINOR.PATCH, refusing anything else.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", s)
	}
	var n [3]int
	for i := range n {
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("%q: %w", s, err)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

// MustParseVersion is ParseVersion for a string the source itself carries.
func MustParseVersion(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(fmt.Errorf("compat: %w", err))
	}
	return v
}

// String is the version as written in VERSION.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Less reports whether v is an older version than o: the first of major, minor
// and patch that differs decides. It is one three-way compare rather than a
// ladder of != and <, because a < inside a branch that already knows the two
// numbers differ is a comparison no test can tell from <=.
func (v Version) Less(o Version) bool {
	return cmp.Or(
		cmp.Compare(v.Major, o.Major),
		cmp.Compare(v.Minor, o.Minor),
		cmp.Compare(v.Patch, o.Patch),
	) < 0
}

var binary = MustParseVersion(buildinfo.Version())

// Binary is the version this build carries.
func Binary() Version { return binary }

// Requirement is a repo's declared oldest acceptable binary.
type Requirement struct {
	Min     Version
	operand string
}

// String is the requirement as a repo would write it: ">=1.4".
func (r Requirement) String() string { return ">=" + r.operand }

var requiresPattern = regexp.MustCompile(`^>=\s*([0-9]+\.[0-9]+(?:\.[0-9]+)?)$`)

// ParseRequires reads `>=MAJOR.MINOR` or `>=MAJOR.MINOR.PATCH`, the one form
// there is, like go.mod's go line. Anything else is refused with the form
// that works: a constraint this binary cannot compare must not be guessed at.
func ParseRequires(s string) (Requirement, error) {
	bad := fmt.Errorf("requires = %q is not a version this binary can compare; write the oldest version the repo accepts, like requires = \">=1.4\"", s)
	m := requiresPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Requirement{}, bad
	}
	operand := m[1]
	full := operand
	if strings.Count(operand, ".") == 1 {
		full += ".0"
	}
	min, err := ParseVersion(full)
	if err != nil {
		return Requirement{}, bad
	}
	return Requirement{Min: min, operand: operand}, nil
}
