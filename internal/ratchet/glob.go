package ratchet

import (
	"fmt"
	"strings"
)

// ChangedScope names how a law's [scope] narrows its file set to a
// diff-relational input — what a COMMIT changed — rather than the whole tree
// every other scope judges. Empty (ChangedNone) is that ordinary tree-state
// scope; a law with either other value answers nothing unless the caller
// supplies the matching Options field (StagedFiles/LaneFiles) and a
// pre-image reader, exactly like symbol-removed answers nothing without
// --base.
type ChangedScope string

const (
	// ChangedNone is no diff restriction: every other scope's tree-state
	// question, unchanged.
	ChangedNone ChangedScope = ""
	// ChangedStaged is the files THIS COMMIT stages (Options.StagedFiles),
	// pre-image at Options.Base (HEAD at commit time).
	ChangedStaged ChangedScope = "staged"
	// ChangedLane is the files the current lane has changed since it branched
	// (Options.LaneFiles), pre-image at Options.LaneBase (the lane's
	// merge-base).
	ChangedLane ChangedScope = "lane"
)

// Scope is the set of files a law judges: slash-relative globs against the
// repo root. Exclude always wins, so a law can name a broad include and carve
// out the trees (build output, the guards' own fixtures) it must not read.
type Scope struct {
	Include []string
	Exclude []string
	// Alias names a set declared in the repo's `.ratchet/scopes.toml`
	// (`[sets] <alias> = [glob, ...]`), resolved into Include at load time —
	// LoadLaws prepends the named set's globs to whatever Include this scope
	// also declares, so several laws sharing a boundary (Tier-1, presentation)
	// state it once. Kept after resolution so a caller can see which alias, if
	// any, a scope came from.
	Alias string
	// IgnoreGitignore walks files git ignores. The walk is gitignore-aware so
	// no law has to enumerate build output, but a repo that ignores a whole
	// extension (borld ignores `*.md` for generated design pages) hides files
	// a doc law is entirely about — and the fix must not be to weaken the
	// repo's .gitignore for the guard's benefit.
	IgnoreGitignore bool
	// MinFiles is the floor below which a clean verdict is not a verdict: a
	// law whose scope silently stopped matching (a crate renamed, a typo in a
	// glob) reports green over files it never opened.
	MinFiles int
	// Changed narrows the file set to a diff-relational input instead of tree
	// state — see ChangedScope.
	Changed ChangedScope
}

// parseScope reads a law's [scope] table. Lives beside Scope itself rather
// than in law.go's own top-level key parsing, which every other [scope] key
// also does — scope parsing is one mechanism, not a table's worth of law.go.
func parseScope(doc *tomlDoc) (Scope, error) {
	if !doc.has("scope") {
		return Scope{}, fmt.Errorf("missing [scope] — a law must say which files it judges")
	}
	var s Scope
	for _, k := range doc.keys("scope") {
		v, _ := doc.value("scope", k)
		if k == "min_files" {
			if v.kind != tomlInt || v.i < 0 {
				return Scope{}, fmt.Errorf("scope.min_files is a non-negative integer")
			}
			s.MinFiles = v.i
			continue
		}
		if k == "ignore_gitignore" {
			if v.kind != tomlBool {
				return Scope{}, fmt.Errorf("scope.ignore_gitignore is a boolean, got %s", v.kind)
			}
			s.IgnoreGitignore = v.b
			continue
		}
		if k == "alias" {
			if v.kind != tomlString || v.s == "" {
				return Scope{}, fmt.Errorf("scope.alias is a non-empty string naming a set in %s", ScopesFile)
			}
			s.Alias = v.s
			continue
		}
		if k == "changed" {
			switch ChangedScope(v.s) {
			case ChangedStaged, ChangedLane:
				s.Changed = ChangedScope(v.s)
			default:
				return Scope{}, fmt.Errorf("scope.changed = %q — a changed scope is %q or %q", v.s, ChangedStaged, ChangedLane)
			}
			continue
		}
		if v.kind != tomlArray {
			return Scope{}, fmt.Errorf("scope.%s is an array of globs, got %s", k, v.kind)
		}
		switch k {
		case "include":
			s.Include = v.list
		case "exclude":
			s.Exclude = v.list
		default:
			return Scope{}, fmt.Errorf("unknown key scope.%s — [scope] takes include, exclude, alias, ignore_gitignore, min_files and changed", k)
		}
	}
	if len(s.Include) == 0 && s.Alias == "" {
		return Scope{}, fmt.Errorf("scope.include is required and must name at least one glob, or scope.alias a set in %s", ScopesFile)
	}
	return s, nil
}

// ExplicitPaths are the include entries that name ONE file rather than a set.
// A glob that matches nothing is a scope that shrank; a literal path that is
// missing is a citation that broke, and the two deserve different words.
func (s Scope) ExplicitPaths() []string {
	var out []string
	for _, p := range s.Include {
		if !strings.ContainsAny(p, "*?[") {
			out = append(out, p)
		}
	}
	return out
}

// Matches reports whether one repo-relative path is in scope.
func (s Scope) Matches(path string) bool {
	p := normalizeSlashes(path)
	for _, pattern := range s.Exclude {
		if matchGlob(pattern, p) {
			return false
		}
	}
	for _, pattern := range s.Include {
		if matchGlob(pattern, p) {
			return true
		}
	}
	return false
}

// couldMatchUnder reports whether any file BELOW dir could still be in scope,
// which is what makes the walk cheap: a `crates/**/*.rs` law never opens
// `node_modules/`, but must still descend `crates/` itself even though the
// directory is not a match.
func (s Scope) couldMatchUnder(dir string) bool {
	d := normalizeSlashes(dir)
	for _, pattern := range s.Exclude {
		if matchGlob(pattern, d) || matchGlob(pattern, d+"/") {
			return false
		}
	}
	for _, pattern := range s.Include {
		if prefixCouldMatch(pattern, d) {
			return true
		}
	}
	return false
}

func normalizeSlashes(p string) string {
	return strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./")
}

// prefixCouldMatch reports whether pattern could match some path under dir, by
// walking the pattern's segments against dir's. A `**` segment matches any
// remaining depth, so everything below it is still reachable.
func prefixCouldMatch(pattern, dir string) bool {
	pat := strings.Split(pattern, "/")
	seg := strings.Split(dir, "/")
	if dir == "" {
		return true
	}
	i := 0
	for _, s := range seg {
		if i >= len(pat) {
			return false
		}
		if pat[i] == "**" {
			return true
		}
		if !matchSegment(pat[i], s) {
			return false
		}
		i++
	}
	return true
}

// matchGlob matches a slash-separated path against a glob supporting `*`
// (within one segment), `?` (one character) and `**` (any number of segments,
// including none).
func matchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// `**` at the end matches everything below, but must still have
			// something below it: `crates/tests/**` is about the directory's
			// CONTENTS, which is why an empty remainder fails here.
			if len(pat) == 1 {
				return len(seg) > 0
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 || !matchSegment(pat[0], seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

// matchSegment matches one path segment against a pattern segment (`*`, `?`).
func matchSegment(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	// Iterative backtracking so a pattern with several `*` stays linear-ish and
	// never recurses per character.
	var star, mark = -1, 0
	i, j := 0, 0
	for i < len(s) {
		switch {
		case j < len(pattern) && (pattern[j] == '?' || pattern[j] == s[i]):
			i++
			j++
		case j < len(pattern) && pattern[j] == '*':
			star, mark = j, i
			j++
		case star >= 0:
			mark++
			i, j = mark, star+1
		default:
			return false
		}
	}
	for j < len(pattern) && pattern[j] == '*' {
		j++
	}
	return j == len(pattern)
}
