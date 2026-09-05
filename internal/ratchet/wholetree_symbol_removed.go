package ratchet

import (
	"fmt"
	"regexp"
	"sort"
)

// --- symbol-removed ----------------------------------------------------

// symbolRemovedTombstoneRe is the tombstone marker this kind accepts, fixed
// by the engine as `// ratchet: <law name> <symbol>: <reason>` (or `#`) —
// requiring a non-empty reason after the colon tells a genuine admission
// apart from a stub nobody filled in.
func symbolRemovedTombstoneRe(lawName string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*(?://|#)[ \t]*ratchet:[ \t]*` + regexp.QuoteMeta(lawName) + `[ \t]+([A-Za-z0-9_]+):[ \t]*\S`)
}

// symbolRemovedHits reports every symbol law.Matcher.Pattern captured at
// BASE that is absent from every in-scope file at TIP and carries no
// tombstone, keyed `<base path>:<name>` — the base path is what a person
// restores the symbol to, so a plain rename reports under its OLD name
// while a move to a different file, name unchanged, reports nothing.
func symbolRemovedHits(law Law, base BaseReader, files []string, content map[string]string) ([]Hit, error) {
	tombstoneRe := symbolRemovedTombstoneRe(law.Name)
	tipNames, tombstoned := map[string]bool{}, map[string]bool{}
	for _, rel := range files {
		if !law.Scope.Matches(rel) {
			continue
		}
		text, ok := content[rel]
		if !ok {
			continue
		}
		for _, line := range splitLines(text) {
			if m := law.Matcher.Pattern.FindStringSubmatch(line); m != nil {
				tipNames[m[1]] = true
			}
		}
		for _, m := range tombstoneRe.FindAllStringSubmatch(text, -1) {
			tombstoned[m[1]] = true
		}
	}

	basePaths, err := base.List()
	if err != nil {
		return nil, fmt.Errorf("law %q: reading base tree: %w", law.Name, err)
	}
	sort.Strings(basePaths)
	var inScope []string
	for _, rel := range basePaths {
		if law.Scope.Matches(rel) {
			inScope = append(inScope, rel)
		}
	}
	// One batch read for every in-scope base path, instead of one process
	// per file — the reason a diff-scoped law over hundreds of files stays
	// cheap. A path List() named but ReadAll comes back without (vanished
	// between the two calls) is skipped, not an error, the same tolerance a
	// mid-walk vanish already gets elsewhere in this package.
	contents, err := base.ReadAll(inScope)
	if err != nil {
		return nil, fmt.Errorf("law %q: reading base tree: %w", law.Name, err)
	}
	var hits []Hit
	for _, rel := range inScope {
		data, ok := contents[rel]
		if !ok {
			continue
		}
		for _, line := range splitLines(string(data)) {
			m := law.Matcher.Pattern.FindStringSubmatch(line)
			if m == nil || tipNames[m[1]] || tombstoned[m[1]] {
				continue
			}
			hits = append(hits, Hit{
				Law: law.Name, File: rel, Weight: 1,
				Key:  rel + ":" + m[1],
				What: fmt.Sprintf("%s is gone from %s with no tombstone", m[1], rel),
			})
		}
	}
	return hits, nil
}

// symbolRemovedLawHits is Check()'s entry point: with no base to compare
// against, the law answers nothing rather than guessing, and notes the
// skip once so a report-only run does not read as a rule that never fired.
// An unresolvable base REF (an unborn branch, a typo'd ref) is treated the
// same way — every other law still runs, rather than the whole commit
// blocking on a git failure this one kind cannot recover from.
func symbolRemovedLawHits(law Law, base BaseReader, baseRef string, files []string, content map[string]string, res *Result) ([]Hit, error) {
	if base == nil {
		res.Notes = append(res.Notes, law.Name+": skipped, no base (pass --base <ref>)")
		return nil, nil
	}
	hits, err := symbolRemovedHits(law, base, files, content)
	if err != nil && baseRefNotFound(err) {
		res.Notes = append(res.Notes, fmt.Sprintf("%s: skipped, base %s not found (pass --base <ref>)", law.Name, baseRef))
		return nil, nil
	}
	return hits, err
}
