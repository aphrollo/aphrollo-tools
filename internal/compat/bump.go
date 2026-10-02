package compat

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Bump is how far a change moves the version.
type Bump string

const (
	BumpNone  Bump = "none"
	BumpPatch Bump = "patch"
	BumpMinor Bump = "minor"
	BumpMajor Bump = "major"
)

// bumpValues is the one list a refusal offers.
const bumpValues = "none, patch, minor or major"

// The two files a version owes: where it lives, named in a refusal so the fix
// is one edit, and the changelog that has to explain it.
const (
	VersionFile   = "internal/buildinfo/VERSION"
	ChangelogFile = "CHANGELOG.md"
)

var bumpLine = regexp.MustCompile(`(?mi)^[ \t]*version:[ \t]*(none|patch|minor|major)\b`)

// DeclaredBump reads the decision a PR body states in a line of its own,
// `version: none|patch|minor|major`, in any case and with any reason after it.
// A change that moves what a consumer sees is a minor bump at least, and a
// rule that is only a habit gets forgotten: the author says it in the body,
// and the check below holds the file to it.
func DeclaredBump(body string) (Bump, error) {
	var found []Bump
	for _, m := range bumpLine.FindAllStringSubmatch(body, -1) {
		if b := Bump(strings.ToLower(m[1])); !slices.Contains(found, b) {
			found = append(found, b)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("the PR body has no `version:` line; add one saying what this change does to the version (%s)", bumpValues)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("the PR body's `version:` lines disagree: it says both %s and %s; keep one (%s)", found[0], found[1], bumpValues)
}

// BumpBetween names the first number of the version that moved from base to
// head, and refuses a head older than its base.
func BumpBetween(base, head Version) (Bump, error) {
	switch {
	case head.Less(base):
		return "", fmt.Errorf("VERSION went backwards: %s -> %s", base, head)
	case head.Major != base.Major:
		return BumpMajor, nil
	case head.Minor != base.Minor:
		return BumpMinor, nil
	case head.Patch != base.Patch:
		return BumpPatch, nil
	}
	return BumpNone, nil
}

// surfacePrefixes are the sources that ARE a verdict a consumer's gate gives:
// the law presets a repo adopts, the language rows and their parser, and the
// masks that decide what text a law reads. A change to one moves a verdict by
// construction, which is what lets a path rule stand in for judgment here. The
// rest of the gate (its stages, its mutation run) can change a verdict with no
// path to say so, and is left to the author's `version:` line.
var surfacePrefixes = []string{"internal/ratchet/presets/", "internal/lang/", "internal/mask/"}

// VerdictSurface reports whether file, a repo-relative slash path, is source
// of a consumer-visible verdict. A test file and a fixture under testdata
// change no verdict a consumer sees.
func VerdictSurface(file string) bool {
	if strings.HasSuffix(file, "_test.go") || strings.Contains(file, "/testdata/") {
		return false
	}
	for _, p := range surfacePrefixes {
		if strings.HasPrefix(file, p) {
			return true
		}
	}
	return false
}

// JudgeBump holds a PR to the version rule and returns every way it fails it,
// none when it holds. The body must state its bump, the VERSION file must
// carry exactly that bump from base to head, and a change to a verdict surface
// must be a minor bump at least.
func JudgeBump(base, head Version, body string, changed []string) []string {
	var problems []string
	declared, declErr := DeclaredBump(body)
	if declErr != nil {
		problems = append(problems, declErr.Error())
	}
	actual, actualErr := BumpBetween(base, head)
	if actualErr != nil {
		problems = append(problems, actualErr.Error())
	}
	if declErr == nil && actualErr == nil && declared != actual {
		problems = append(problems, fmt.Sprintf("the body says `version: %s` but VERSION went %s -> %s, which is %s: bump %s or change the line", declared, base, head, actual, VersionFile))
	}
	if actualErr == nil && (actual == BumpNone || actual == BumpPatch) {
		for _, p := range changed {
			if VerdictSurface(p) {
				problems = append(problems, fmt.Sprintf("%s changes what a consumer's gate says (a law, a language row or a mask), so this is at least a minor bump, and VERSION went %s -> %s (%s)", p, base, head, actual))
				break
			}
		}
	}
	return problems
}
