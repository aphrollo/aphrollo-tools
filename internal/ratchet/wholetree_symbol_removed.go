package ratchet

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// --- symbol-removed ----------------------------------------------------

// symbolRemovedTombstoneRe is the tombstone marker this kind accepts, fixed
// by the engine as `// ratchet: <law name> <symbol>: <reason>` (or `#`) —
// requiring a non-empty reason after the colon tells a genuine admission
// apart from a stub nobody filled in. What the marker names is either one
// SYMBOL or one repo-relative PATH (see symbolRemovedClaimsAPath), so the
// captured group carries `/`, `.` and `-` as well.
func symbolRemovedTombstoneRe(lawName string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*(?://|#)[ \t]*ratchet:[ \t]*` + regexp.QuoteMeta(lawName) + `[ \t]+([A-Za-z0-9_][A-Za-z0-9_./-]*):[ \t]*\S`)
}

// symbolRemovedClaimsAPath tells the two things a tombstone can name apart: a
// symbol is a bare identifier, so anything carrying a `/` or a `.` is a
// repo-relative path instead. That is the whole classification — a language
// whose test names contain a dot would need a different one, and none of the
// preset patterns capture such a name.
func symbolRemovedClaimsAPath(claim string) bool {
	return strings.ContainsAny(claim, "/.")
}

// symbolRemovedFileAdmitted reports whether a file tombstone naming rel
// admits every symbol that stood in rel at base — the #574 case, a whole test
// file deleted along with the subject its tests exercised, where one line
// saying so beats one line per test saying nothing a reader learns from.
//
// It is deliberately the narrowest reading of that claim, because a bulk
// admission is also what a cheat looks like. Both conditions are checkable
// facts about the tip, not statements of intent:
//
//   - rel must be GONE at tip. A file still standing admits nothing: deleting
//     the one failing test out of a surviving file is the removal this law
//     exists to report, and it still needs a tombstone naming that test.
//   - NOTHING captured in rel at base may survive anywhere at tip. A file
//     whose tests turn up elsewhere was split, not retired, and a test dropped
//     on the way out is exactly the evidence a file-level line would hide.
func symbolRemovedFileAdmitted(rel string, baseNames []string, tipNames, tipPaths, fileTombstoned map[string]bool) bool {
	if !fileTombstoned[rel] || tipPaths[rel] {
		return false
	}
	for _, name := range baseNames {
		if tipNames[name] {
			return false
		}
	}
	return true
}

// wholeFileSymbolPattern recompiles a symbol-removed law's pattern to match
// against a whole file's text rather than one physical line at a time — the
// idiomatic Rust layout puts `#[test]` on its own line above the `fn` it
// marks, and a line-by-line scan never joins the two into one match. `(?m)`
// is prepended so a pattern anchored with `^`/`$` still binds to each
// line's start/end within the file rather than the file's as a whole (a Go
// test law's `^func (Test...)\(` keeps matching once per line); a pattern
// that already opens with its own `(?...)` flag group is trusted to have
// chosen its own semantics and is left as-is.
func wholeFileSymbolPattern(p *regexp.Regexp) *regexp.Regexp {
	src := p.String()
	if strings.HasPrefix(src, "(?") {
		return p
	}
	return regexp.MustCompile("(?m)" + src)
}

// symbolRemovedHits reports every symbol law.Matcher.Pattern captured at
// BASE that is absent from every in-scope file at TIP and carries no
// tombstone, keyed `<base path>:<name>` — the base path is what a person
// restores the symbol to, so a plain rename reports under its OLD name
// while a move to a different file, name unchanged, reports nothing. The
// pattern is matched against each file's whole text (see
// wholeFileSymbolPattern), not one physical line at a time.
//
// A tombstone naming a PATH rather than a symbol admits a whole file's
// removed symbols at once, under the two conditions symbolRemovedFileAdmitted
// checks.
func symbolRemovedHits(law Law, base BaseReader, files []string, content map[string]string) ([]Hit, error) {
	pattern := wholeFileSymbolPattern(law.Matcher.Pattern)
	tombstoneRe := symbolRemovedTombstoneRe(law.Name)
	tipNames, tombstoned := map[string]bool{}, map[string]bool{}
	// tipPaths is every in-scope path the tip HAS, recorded before the
	// content lookup below: a path present but unread still counts as
	// standing, so a file tombstone naming it is refused rather than honored
	// on a file nobody looked at.
	tipPaths, fileTombstoned := map[string]bool{}, map[string]bool{}
	for _, rel := range files {
		if !law.Scope.Matches(rel) {
			continue
		}
		tipPaths[rel] = true
		text, ok := content[rel]
		if !ok {
			continue
		}
		for _, idx := range pattern.FindAllStringSubmatchIndex(text, -1) {
			tipNames[text[idx[2]:idx[3]]] = true
		}
		for _, m := range tombstoneRe.FindAllStringSubmatch(text, -1) {
			if symbolRemovedClaimsAPath(m[1]) {
				fileTombstoned[m[1]] = true
				continue
			}
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
		text := string(data)
		var baseNames []string
		for _, idx := range pattern.FindAllStringSubmatchIndex(text, -1) {
			baseNames = append(baseNames, text[idx[2]:idx[3]])
		}
		if symbolRemovedFileAdmitted(rel, baseNames, tipNames, tipPaths, fileTombstoned) {
			continue
		}
		for _, name := range baseNames {
			if tipNames[name] || tombstoned[name] {
				continue
			}
			hits = append(hits, Hit{
				Law: law.Name, File: rel, Weight: 1,
				Key:  rel + ":" + name,
				What: fmt.Sprintf("%s is gone from %s with no tombstone", name, rel),
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
