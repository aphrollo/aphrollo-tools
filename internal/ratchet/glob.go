package ratchet

import "strings"

// Scope is the set of files a law judges: slash-relative globs against the
// repo root. Exclude always wins, so a law can name a broad include and carve
// out the trees (build output, the guards' own fixtures) it must not read.
type Scope struct {
	Include []string
	Exclude []string
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
