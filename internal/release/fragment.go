// Package release is how a version comes to exist without a PR carrying it.
// A PR that changes what a consumer sees adds one changelog fragment
// (changelog.d/<slug>.md) saying so and how big the change is; the release
// workflow reads the fragments merged since the newest release tag, tags the
// next version from the biggest level among them, and the full changelog is
// assembled from the fragments each tag first contains. Everything here is a
// pure function over names, levels and text: the git reads live in the cli.
package release

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

// FragmentDir is where a PR's changelog fragment lives, one file per lane.
const FragmentDir = "changelog.d"

// Fragment is one PR's entry for the changelog: the level of the release it
// asks for, and the words a consumer reads.
type Fragment struct {
	// Name is the file name without .md, the lane or slug that wrote it.
	Name string
	// Level is patch, minor or major; a change of level none adds no fragment.
	Level compat.Bump
	// Body is the words under the level line, trimmed.
	Body string
}

var (
	levelLine   = regexp.MustCompile(`(?i)^level:[ \t]*([a-z]*)[ \t]*$`)
	headingLine = regexp.MustCompile(`^#{1,2}[ \t]`)
	slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// FragmentPath is the repo path of the fragment named name.
func FragmentPath(name string) string { return FragmentDir + "/" + name + ".md" }

// InFragmentDir reports whether file, a repo-relative slash path, is under the
// fragment directory.
func InFragmentDir(file string) bool {
	_, under := strings.CutPrefix(file, FragmentDir+"/")
	return under
}

// FragmentName is the name of the fragment a repo-relative slash path is, and ok is false for
// a path that is not one: not directly in the fragment directory, not .md, not
// a plain slug, or the directory's own README.
func FragmentName(repoFile string) (name string, ok bool) {
	file, found := strings.CutPrefix(repoFile, FragmentDir+"/")
	if !found {
		return "", false
	}
	name, isMarkdown := strings.CutSuffix(file, ".md")
	if !isMarkdown || name == "README" || !slugPattern.MatchString(name) {
		return "", false
	}
	return name, true
}

// ParseFragment reads a fragment's text: a first line `level: patch|minor|major`
// and, under it, what a consumer will notice. The words go into the assembled
// changelog under the release's own heading, so a level-1 or level-2 heading in
// them would open a section that is no release; a level-3 heading is the
// fragment's own structure.
func ParseFragment(name, text string) (Fragment, error) {
	where := FragmentPath(name)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	first := 0
	for first < len(lines) && strings.TrimSpace(lines[first]) == "" {
		first++
	}
	if first == len(lines) {
		return Fragment{}, fmt.Errorf("%s is empty: it starts with a `level:` line, then what a consumer will notice", where)
	}
	m := levelLine.FindStringSubmatch(strings.TrimSpace(lines[first]))
	if m == nil {
		return Fragment{}, fmt.Errorf("%s starts with a `level:` line (level: patch, minor or major), then what a consumer will notice; it starts with %q", where, strings.TrimSpace(lines[first]))
	}
	level := compat.Bump(strings.ToLower(m[1]))
	switch level {
	case compat.BumpPatch, compat.BumpMinor, compat.BumpMajor:
	case compat.BumpNone:
		return Fragment{}, fmt.Errorf("%s says level: none, and a change of level none adds no fragment: delete the file and say `version: none` in the PR body", where)
	default:
		return Fragment{}, fmt.Errorf("%s says level: %s; the level is patch, minor or major", where, m[1])
	}
	rest := lines[first+1:]
	body := strings.TrimSpace(strings.Join(rest, "\n"))
	if body == "" {
		return Fragment{}, fmt.Errorf("%s has no words under its level line: write what a consumer will notice and what migrates by itself", where)
	}
	for _, line := range rest {
		if headingLine.MatchString(line) {
			return Fragment{}, fmt.Errorf("%s has a changelog heading %q: the release owns the headings, so write plain paragraphs or bullets (a ### heading is fine)", where, strings.TrimSpace(line))
		}
	}
	return Fragment{Name: name, Level: level, Body: body}, nil
}
