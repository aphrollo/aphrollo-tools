package ratchet

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// wholetree_identresolves.go answers ident-resolves — see check.go's
// dispatch and fixtures.go's fixtureWholeTreeHits for the two callers.
//
// KindIdentResolves: a backticked identifier in a Go comment or in a *.md
// file (`CamelCase`, `snake_case(`, `pkg.Name`, or a bare hyphenated/
// underscored TOML-key-shaped token) must name a real declaration — a Go
// func/method/type/const/var, or a key any tracked TOML file sets. A
// backticked `key = value` claim is checked further: the key must exist AND
// its claimed value must match the TOML file's actual value, verbatim as
// text — the shape #191 needed (a comment claiming `mutants-local = false`
// while aphrollo.toml sets it `true`). Whole-tree because the declaration a
// citation names can live in any file, not only the one making the claim —
// see identResolvesHits.
const KindIdentResolves MatcherKind = "ident-resolves"

// declIndex is the whole-tree declaration table ident-resolves checks a
// citation against, built once from every file the law's fixture or scan
// handed it (never scope-filtered: a citation in a *.md file legitimately
// names a declaration in an internal/ package the law's own [scope] never
// walks for CITATIONS, but the declaration still has to be indexed).
type declIndex struct {
	// names is every declared Go identifier (func, method — by name alone,
	// receiver type dropped — type, const, var), package distinction dropped:
	// a `pkg.Name` citation is resolved by NAME only, the same locality
	// tradeoff doc-path-resolves' unit-relative fallback makes rather than
	// building a real per-package symbol table.
	names map[string]bool
	// toml is every key any tracked *.toml file sets, to its raw value text
	// (trimmed, comment stripped) — last file to declare a key wins, since a
	// key = value claim is a repo-wide fact, not a per-file one.
	toml map[string]string
}

// goDeclPattern recognizes one Go top-level declaration per line: a func
// (bare or with a method receiver, capturing the method name only, never the
// receiver type) in group 1, or a type/const/var name in group 2. It is a
// line-oriented heuristic, not a parser — a multi-line `const (`/`var (`
// block's inner lines are not indexed, the same simplification
// KindLineCount's codeLines makes for comment runs rather than a real
// tokenizer.
var goDeclPattern = regexp.MustCompile(
	`^func\s*(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(|^(?:type|const|var)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)

// tomlKVPattern recognizes one `key = value` line in a TOML file — table
// headers and comments are filtered by the caller before this ever runs.
var tomlKVPattern = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*=\s*(.+?)\s*$`)

// buildDeclIndex scans every file's content for a declaration, regardless of
// any law's [scope]: the citations a law scans are scope-restricted, the
// declarations they must resolve against are not.
func buildDeclIndex(files []string, content map[string]string) declIndex {
	idx := declIndex{names: map[string]bool{}, toml: map[string]string{}}
	for _, rel := range files {
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".go":
			for _, line := range splitLines(content[rel]) {
				m := goDeclPattern.FindStringSubmatch(strings.TrimSpace(line))
				if m == nil {
					continue
				}
				if name := lastCapture(m); name != "" {
					idx.names[name] = true
				}
			}
		case ".toml":
			for _, line := range splitLines(content[rel]) {
				t := strings.TrimSpace(line)
				if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
					continue
				}
				if m := tomlKVPattern.FindStringSubmatch(t); m != nil {
					idx.toml[m[1]] = stripTrailingComment(m[2])
				}
			}
		}
	}
	return idx
}

// stripTrailingComment drops a TOML `# ...` trailing comment from an already
// key-stripped value, the same quote-unaware simplification tomlKVPattern's
// caller already accepts (a `#` inside a quoted string value is the one case
// this misreads, and no law fixture exercises that shape).
func stripTrailingComment(value string) string {
	if i := strings.Index(value, "#"); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}

// kvClaimPattern recognizes a backticked citation shaped as a config claim:
// `key = value`, distinguished from a bare identifier by the literal `=`.
var kvClaimPattern = regexp.MustCompile(`^([a-z][a-z0-9_-]*)\s*=\s*(.+)$`)

// resolve reports whether cited — the text a citation's backticks wrapped,
// with the backticks themselves already stripped — names something real. A
// `key = value` claim resolves only when the key exists AND its claimed
// value matches the TOML file's actual value, verbatim as text; every other
// shape resolves when the name (paren and package prefix stripped) is a
// declared Go identifier or a TOML key.
func (idx declIndex) resolve(cited string) (ok bool, what string) {
	if m := kvClaimPattern.FindStringSubmatch(cited); m != nil {
		key, claimed := m[1], strings.TrimSpace(m[2])
		actual, exists := idx.toml[key]
		if !exists {
			return false, fmt.Sprintf("%s is not a key any tracked TOML file sets", key)
		}
		if actual != claimed {
			return false, fmt.Sprintf("claims %s = %s, but the TOML file sets %s = %s", key, claimed, key, actual)
		}
		return true, ""
	}
	name := strings.TrimSuffix(cited, "(")
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	if _, isTOMLKey := idx.toml[name]; idx.names[name] || isTOMLKey {
		return true, ""
	}
	return false, fmt.Sprintf("%s is not a declared identifier or TOML key", name)
}

// identResolvesHits scans every file in law's scope for a backticked
// citation and reports one it cannot resolve. A *.go file is read through
// its trailing `//` comment only — code and string literals are never a
// citation's home — a *.md (or other non-.go) file is read whole, since
// prose carries no code/comment distinction.
func identResolvesHits(law Law, files []string, content map[string]string) []Hit {
	idx := buildDeclIndex(files, content)
	var hits []Hit
	for _, rel := range files {
		if !law.Scope.Matches(rel) {
			continue
		}
		raw := splitLines(content[rel])
		goFile := strings.ToLower(filepath.Ext(rel)) == ".go"
		for i, line := range raw {
			scanned := line
			if goFile {
				_, scanned = splitTrailingComment(line, "//")
				if scanned == "" {
					continue
				}
			}
			if law.excluded(line) {
				continue
			}
			for _, loc := range law.Matcher.Pattern.FindAllStringSubmatchIndex(scanned, -1) {
				gStart, gEnd := loc[len(loc)-2], loc[len(loc)-1]
				if gStart < 0 {
					continue
				}
				if law.escaped(rel, raw, i) {
					continue
				}
				cited := scanned[gStart:gEnd]
				if ok, what := idx.resolve(cited); !ok {
					hits = append(hits, law.hit(rel, i+1, what))
				}
			}
		}
	}
	return hits
}
