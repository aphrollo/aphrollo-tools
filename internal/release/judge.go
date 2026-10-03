package release

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

// FileChange is one file a PR touched, measured from the merge base: Status is
// 'A' (added), 'M' (modified) or 'D' (deleted); File is its repo-relative slash
// path.
type FileChange struct {
	Status byte
	File   string
}

// Change is everything the version rule judges about a PR.
type Change struct {
	// Body is the PR body, which carries the `version:` line.
	Body string
	// Files are the files the PR touched since it left the base.
	Files []FileChange
	// Fragments holds the text, at the PR's head, of each fragment file the PR added.
	Fragments map[string]string
	// BaseChangelog and HeadChangelog are CHANGELOG.md at the merge base and at the head.
	BaseChangelog, HeadChangelog string
}

// JudgeChange holds a PR to the version rule and returns every way it fails it,
// none when it holds. A PR states the version it asks for in its body and
// carries that as one changelog fragment; it never carries a version number:
//
//   - the body states `version: none|patch|minor|major`;
//   - the version file is not added or edited (a PR may delete it);
//   - CHANGELOG.md's released sections are not touched, and a fragment already
//     merged is not edited or deleted;
//   - a non-none PR adds exactly one changelog.d/<slug>.md whose level is the
//     body's, and a none PR adds none;
//   - a change to what a consumer's gate says (a law, a language row, a mask) is
//     at least a minor change.
func JudgeChange(c Change) []string {
	var problems []string
	declared, declErr := compat.DeclaredBump(c.Body)
	if declErr != nil {
		problems = append(problems, declErr.Error())
	}

	var addedFragments []string
	for _, f := range c.Files {
		switch {
		case f.File == compat.VersionFile && f.Status != 'D':
			problems = append(problems, fmt.Sprintf("%s is not edited by a PR: a version comes from the release tag the merge earns, never from a file; drop this change (deleting the file is fine)", compat.VersionFile))
		case InFragmentDir(f.File) && f.File != FragmentDir+"/README.md" && f.Status == 'A':
			if _, ok := FragmentName(f.File); !ok {
				problems = append(problems, fmt.Sprintf("%s is not a fragment: a fragment is %s, a plain name with no directory", f.File, FragmentPath("<slug>")))
				continue
			}
			addedFragments = append(addedFragments, f.File)
		case InFragmentDir(f.File) && f.File != FragmentDir+"/README.md":
			problems = append(problems, fmt.Sprintf("%s is a released fragment: it is the history of a release, so a PR neither edits nor deletes it", f.File))
		}
	}
	for _, v := range ChangedSections(c.BaseChangelog, c.HeadChangelog) {
		problems = append(problems, fmt.Sprintf("CHANGELOG.md's section for %s changed: released sections are frozen, and a later release is written as a changelog.d fragment", v))
	}

	if declErr == nil {
		problems = append(problems, judgeFragments(declared, addedFragments, c.Fragments)...)
		if declared == compat.BumpNone || declared == compat.BumpPatch {
			for _, f := range c.Files {
				if compat.VerdictSurface(f.File) {
					problems = append(problems, fmt.Sprintf("%s changes what a consumer's gate says (a law, a language row or a mask), so this is at least a minor change, and the body says `version: %s`", f.File, declared))
					break
				}
			}
		}
	}
	return problems
}

// judgeFragments holds the fragments a PR added to the level its body states.
func judgeFragments(declared compat.Bump, added []string, text map[string]string) []string {
	var problems []string
	switch {
	case declared == compat.BumpNone && len(added) > 0:
		problems = append(problems, fmt.Sprintf("the body says `version: none` but the PR adds %s: a change nobody sees adds no fragment, or say `version: patch|minor|major`", strings.Join(added, ", ")))
	case declared != compat.BumpNone && len(added) == 0:
		problems = append(problems, fmt.Sprintf("the body says `version: %s` but the PR adds no changelog fragment: add %s starting `level: %s`, then what a consumer will notice and what migrates by itself", declared, FragmentPath("<lane>"), declared))
	case len(added) > 1:
		problems = append(problems, fmt.Sprintf("the PR adds %d changelog fragments (%s): one PR adds exactly one", len(added), strings.Join(added, ", ")))
	}
	if declared == compat.BumpNone {
		return problems
	}
	for _, file := range added {
		name, _ := FragmentName(file)
		frag, err := ParseFragment(name, text[file])
		switch {
		case err != nil:
			problems = append(problems, err.Error())
		case frag.Level != declared:
			problems = append(problems, fmt.Sprintf("%s says `level: %s` but the body says `version: %s`: make them agree", file, frag.Level, declared))
		}
	}
	return problems
}

// Summary is the one line a passing check prints: the level and the fragment
// that carries it.
func Summary(level compat.Bump, fragments []string) string {
	return strings.Join(slices.Concat([]string{string(level)}, fragments), ", ")
}
