package ratchet

import (
	"os"
	"path/filepath"
	"strings"
)

// gitignore answers "would git ignore this path", over the subset of
// .gitignore syntax a source tree actually uses: anchored and unanchored
// patterns, directory-only (`target/`), negation (`!keep.log`), `*`/`?`/`**`.
// The walk needs it because a law's scope is written against the SOURCE tree —
// nobody wants to spell out every build-output directory in every law, and a
// scan that descended `target/` would be slower than the suite it precedes.
type gitignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	pattern string // relative to base, slash form
	base    string // directory the rule was declared in ("" = repo root)
	negate  bool
	dirOnly bool
	rooted  bool // pattern had a leading or embedded slash: anchored at base
}

// loadGitignore reads the repo root's .gitignore plus any nested ones, each
// scoped to its own directory. Build output is skipped while collecting, so a
// stale `target/.gitignore` can never widen the rules.
func loadGitignore(root string) *gitignore {
	ig := &gitignore{}
	ig.rules = append(ig.rules, ignoreRule{pattern: ".git", dirOnly: true})
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() && e.Name() == ".gitignore" {
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err == nil {
					ig.add(rel, string(data))
				}
			}
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() == ".git" {
				continue
			}
			child := path(rel, e.Name())
			if ig.ignored(child, true) {
				continue
			}
			walk(filepath.Join(dir, e.Name()), child)
		}
	}
	walk(root, "")
	return ig
}

func path(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

func (g *gitignore) add(base, text string) {
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{base: base}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimSuffix(line, "/")
		}
		trimmed := strings.TrimPrefix(line, "/")
		r.rooted = strings.HasPrefix(line, "/") || strings.Contains(trimmed, "/")
		r.pattern = trimmed
		if r.pattern == "" {
			continue
		}
		g.rules = append(g.rules, r)
	}
}

// ignored reports whether a repo-relative path is ignored. Later rules win, as
// in git, so a negation can rescue a path an earlier pattern caught.
func (g *gitignore) ignored(rel string, isDir bool) bool {
	rel = normalizeSlashes(rel)
	ignored := false
	for _, r := range g.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if !r.matches(rel) {
			continue
		}
		ignored = !r.negate
	}
	return ignored
}

func (r ignoreRule) matches(rel string) bool {
	if r.base != "" {
		if !strings.HasPrefix(rel, r.base+"/") {
			return false
		}
		rel = rel[len(r.base)+1:]
	}
	if r.rooted {
		return matchGlob(r.pattern, rel) || matchGlob(r.pattern+"/**", rel)
	}
	// An unanchored pattern matches at any depth — `*.log` catches
	// `a/b/run.log`, and `target` catches `crates/pose/target` and everything
	// under it.
	segments := strings.Split(rel, "/")
	for i := range segments {
		if matchGlob(r.pattern, strings.Join(segments[i:], "/")) {
			return true
		}
		if matchGlob(r.pattern, segments[i]) {
			return true
		}
	}
	return false
}
