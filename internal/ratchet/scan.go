package ratchet

import (
	"path/filepath"
	"sort"
)

// The tree walk: resolving every law's scope into one file list, reading
// each file at most once, and serving unchanged files from the mtime cache.
// Split from check.go (which decides what a law's hits MEAN once the walk
// has them) purely to stay under module_size's own recorded ceiling — no
// behavior here changed by the move.

// treeScan is one walk's result: every in-scope file's content-derived hits,
// grouped by law, plus the file contents the registry matcher needs.
type treeScan struct {
	byLaw                  map[string][]Hit
	ignored                map[string]bool
	files                  []string
	content                map[string]string
	scanned, read, matched int
}

// scanTree walks every law's scope ONCE, reading each file at most once and
// serving unchanged files from the mtime cache.
func scanTree(opts Options, laws []Law) (*treeScan, error) {
	scan := &treeScan{byLaw: map[string][]Hit{}, content: map[string]string{}}
	cache := loadCache(opts.CacheDir, opts.Root, laws)

	paths, ignored, err := collectFiles(opts, laws)
	if err != nil {
		return nil, err
	}
	scan.ignored = ignored
	// A registry-both-ways law answers a WHOLE-TREE question, so it needs the
	// raw content of every file in ITS scope. That is a reason to read those
	// files again; it is not a reason to re-run 26 laws' matchers over the
	// whole tree. Keeping the two apart is what makes a repeat run cheap:
	// measured in borld, 26 laws over 1936 files, 5.6s became 1.2s.
	var contentLaws []Law
	for _, l := range laws {
		if l.Matcher.Kind == KindRegistryBothWays {
			contentLaws = append(contentLaws, l)
		}
		// A code-mode line-count law's stale-baseline note needs the actual
		// measured count on a file that no longer produces a hit at all — the
		// cache's "unchanged, no hits" fast path never reads that file's
		// content otherwise.
		if l.Matcher.Kind == KindLineCount && l.Matcher.LineMode == LineCountCode {
			contentLaws = append(contentLaws, l)
		}
		if l.Matcher.Kind == KindSymbolRemoved || l.Matcher.Kind == KindCoChange || l.Matcher.Kind == KindHunkRegex {
			contentLaws = append(contentLaws, l)
		}
	}
	for _, rel := range paths {
		scan.scanned++
		proposed, overlaid := opts.Proposed[rel]
		needsContent := scopedByAny(contentLaws, rel)
		var hits map[string][]Hit
		var ok bool
		if !overlaid {
			hits, ok = cache.lookup(opts.Root, rel)
		}
		content := proposed
		if !overlaid && (!ok || needsContent) {
			data, err := readFile(filepath.Join(opts.Root, filepath.FromSlash(rel)))
			if err != nil {
				if vanished(err) {
					continue // a file that vanished mid-walk is not a finding
				}
				return nil, &ScanReadError{Path: rel, Err: err}
			}
			content = string(data)
			scan.read++
		}
		if needsContent {
			scan.content[rel] = content
		}
		if !ok {
			scan.matched++
			hits = map[string][]Hit{}
			fl := newFileLines(content)
			for _, law := range laws {
				if !law.Scope.Matches(rel) {
					continue
				}
				if h := law.hitsInLines(rel, fl); len(h) > 0 {
					hits[law.Name] = h
				}
			}
			if !overlaid {
				cache.store(opts.Root, rel, hits)
			}
		}
		for name, h := range hits {
			scan.byLaw[name] = append(scan.byLaw[name], h...)
		}
		scan.files = append(scan.files, rel)
	}
	cache.save()
	return scan, nil
}

// scopedByAny reports whether any of these laws claims rel.
func scopedByAny(laws []Law, rel string) bool {
	for _, l := range laws {
		if l.Scope.Matches(rel) {
			return true
		}
	}
	return false
}

// collectFiles is the union of every law's scope, walked once and sorted, plus
// any proposed file that does not exist on disk yet.
func collectFiles(opts Options, laws []Law) ([]string, map[string]bool, error) {
	ignoredFiles := map[string]bool{}
	if len(opts.Files) > 0 {
		return dedupe(append([]string{}, opts.Files...)), ignoredFiles, nil
	}
	// A gitignored path is judged only by a law that opted out, so the walk
	// carries whether it is under one: a repo that ignores a whole extension
	// (borld ignores *.md) would otherwise hide the very files a doc law is about.
	inScope := func(rel string, ignored bool) bool {
		for _, l := range laws {
			if ignored && !l.Scope.IgnoreGitignore {
				continue
			}
			if l.Scope.Matches(rel) {
				return true
			}
		}
		return false
	}
	walkable := func(rel string, ignored bool) bool {
		for _, l := range laws {
			if ignored && !l.Scope.IgnoreGitignore {
				continue
			}
			if l.Scope.couldMatchUnder(rel) {
				return true
			}
		}
		return false
	}

	// A tracked set replaces the walk entirely — but it carries the SAME
	// gitignore flag the walk would have computed, so a law that never opted
	// into ignored files does not suddenly see them just because git tracks
	// them.
	if len(opts.Tracked) > 0 {
		ignoredTracked := map[string]bool{}
		for _, rel := range opts.TrackedIgnored {
			ignoredTracked[normalizeSlashes(rel)] = true
		}
		var out []string
		for _, rel := range opts.Tracked {
			rel = normalizeSlashes(rel)
			if rel == "" || !inScope(rel, ignoredTracked[rel]) {
				continue
			}
			out = append(out, rel)
			if ignoredTracked[rel] {
				ignoredFiles[rel] = true
			}
		}
		for rel := range opts.Proposed {
			if inScope(rel, false) {
				out = append(out, rel)
			}
		}
		sort.Strings(out)
		return dedupe(out), ignoredFiles, nil
	}

	ignore := loadGitignore(opts.Root)
	var out []string
	var walk func(dir, rel string, ignored bool) error
	walk = func(dir, rel string, ignored bool) error {
		entries, err := readDir(dir)
		if err != nil {
			if vanished(err) {
				return nil // a dir that vanished mid-walk is not a finding
			}
			return &ScanReadError{Path: dir, Err: err}
		}
		for _, e := range entries {
			child := path(rel, e.Name())
			if e.IsDir() && e.Name() == ".git" {
				continue // never a subject, and no law may opt into it
			}
			childIgnored := ignored || ignore.ignored(child, e.IsDir())
			if e.IsDir() {
				if !walkable(child, childIgnored) {
					continue
				}
				if err := walk(filepath.Join(dir, e.Name()), child, childIgnored); err != nil {
					return err
				}
				continue
			}
			if inScope(child, childIgnored) {
				out = append(out, child)
				if childIgnored {
					ignoredFiles[child] = true
				}
			}
		}
		return nil
	}
	if err := walk(opts.Root, "", false); err != nil {
		return nil, nil, err
	}
	for rel := range opts.Proposed {
		if inScope(rel, false) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return dedupe(out), ignoredFiles, nil
}

// dedupe sorts and drops consecutive duplicates.
func dedupe(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || in[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}
