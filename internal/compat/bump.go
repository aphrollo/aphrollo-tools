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

// The two files the version rule keeps a PR away from: the version file a build
// no longer reads (a version comes from the release tag), and the changelog,
// whose released sections are frozen.
const (
	VersionFile   = "internal/buildinfo/VERSION"
	ChangelogFile = "CHANGELOG.md"
)

var bumpLine = regexp.MustCompile(`(?mi)^[ \t]*version:[ \t]*(none|patch|minor|major)\b`)

// DeclaredBump reads the decision a PR body states in a line of its own,
// `version: none|patch|minor|major`, in any case and with any reason after it.
// A change that moves what a consumer sees is a minor bump at least, and a
// rule that is only a habit gets forgotten: the author says it in the body,
// and the version check holds the PR to it.
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
