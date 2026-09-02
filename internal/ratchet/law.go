// Package ratchet is the law engine: a consuming repo declares its code laws
// as DATA (`.ratchet/laws/<name>.toml`) and this package scans, judges and
// ratchets them. It exists because the laws previously lived as ~15 hand-written
// Rust test files in the repo they governed — each re-deriving the same scan /
// baseline / escape-comment machinery, each runnable only under `cargo test`,
// which is far too late: a law that can only speak after the write cannot stop
// the write. Here a law is a small declaration, the engine is one place, and
// the same rule runs at pre-edit time against content that is not on disk yet.
package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Severity decides what a NEW hit costs. Deny fails the gate and denies the
// edit that would introduce it; Warn prints once at pre-edit and allows —
// the on-ramp for a law whose baseline is still being paid down.
type Severity int

const (
	Deny Severity = iota
	Warn
)

func (s Severity) String() string {
	if s == Warn {
		return "warn"
	}
	return "deny"
}

// MatcherKind is the shape of rule a law states. Each kind is one of the
// scanning patterns the consuming repo's hand-written guards had converged on.
type MatcherKind string

const (
	// KindLineCount: a file may not exceed `max` lines (module-size debt).
	KindLineCount MatcherKind = "line-count"
	// KindRegexAbsent: a pattern must NOT appear (the bare `.clamp(` guard).
	KindRegexAbsent MatcherKind = "regex-absent"
	// KindRegexPresent: every file in scope MUST contain a pattern (a seeded
	// proptest's explicit seed).
	KindRegexPresent MatcherKind = "regex-present"
	// KindMarkerWithinLines: a `trigger` line requires a `marker` within N
	// lines above it (`// bound:` over a growing collection).
	KindMarkerWithinLines MatcherKind = "marker-within-lines"
	// KindRegistryBothWays: every use is registered and every registry line is
	// used (the dev-instrument registry).
	KindRegistryBothWays MatcherKind = "registry-both-ways"
	// KindDocPathResolves: every cited `.md` path resolves to a file.
	KindDocPathResolves MatcherKind = "doc-path-resolves"
)

// KeyKind is how a hit is IDENTIFIED in the baseline.
type KeyKind string

const (
	// KeyFile counts hits per file: the baseline entry is `<file> | <count>`.
	KeyFile KeyKind = "file"
	// KeyLineContent identifies a hit by its file AND the trimmed text of the
	// offending line, one baseline line per occurrence. Line NUMBERS are
	// deliberately not part of the identity — inserting a line above a hit is
	// not a regression — while swapping one offending site for a different one
	// in the same file IS, which a per-file count cannot see.
	KeyLineContent KeyKind = "file:line-content-hash"
)

// Matcher is a law's one rule.
type Matcher struct {
	Kind    MatcherKind
	Key     KeyKind
	Pattern *regexp.Regexp
	Trigger *regexp.Regexp
	Marker  *regexp.Regexp
	Max     int
	Lines   int
	// Registry fields (KindRegistryBothWays).
	RegistryFile string
	EntryPattern *regexp.Regexp
	UsePattern   *regexp.Regexp
}

// Law is one declared rule, loaded from `.ratchet/laws/<name>.toml`.
type Law struct {
	Name        string
	Description string
	Severity    Severity
	Scope       Scope
	Escape      string
	EscapeLines int
	Baseline    string
	CodeOnly    bool
	Matcher     Matcher
	// Path is the law file itself, so an error can name where the rule came from.
	Path string
	// Source is the law file's text, hashed into the scan cache key: a rule
	// that changed must never be answered from a cache filled under the old one.
	Source string
}

// LawsDir is where a consuming repo keeps its laws, relative to the repo root.
const LawsDir = ".ratchet/laws"

// LoadLaws reads every law under <root>/.ratchet/laws, sorted by name. A repo
// with no laws dir loads zero laws and no error — the engine is opt-in.
func LoadLaws(root string) ([]Law, error) {
	dir := filepath.Join(root, filepath.FromSlash(LawsDir))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var laws []Law
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		law, err := ParseLaw(string(text), strings.TrimSuffix(e.Name(), ".toml"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		law.Path = path
		laws = append(laws, law)
	}
	sort.Slice(laws, func(i, j int) bool { return laws[i].Name < laws[j].Name })
	return laws, nil
}

var rootKeys = map[string]bool{
	"name": true, "description": true, "severity": true, "escape": true,
	"escape_lines": true, "baseline": true, "code_only": true,
}

// matcherKeys is the exact key set each matcher kind accepts, and whether each
// is required. Strictness is the contract: an unknown key is a typo that would
// otherwise silently disable half a rule.
var matcherKeys = map[MatcherKind]map[string]bool{
	KindLineCount:         {"kind": true, "max": true},
	KindRegexAbsent:       {"kind": true, "pattern": true, "key": false},
	KindRegexPresent:      {"kind": true, "pattern": true},
	KindMarkerWithinLines: {"kind": true, "trigger": true, "marker": true, "lines": false},
	KindRegistryBothWays:  {"kind": true, "registry_file": true, "entry_pattern": true, "use_pattern": true},
	KindDocPathResolves:   {"kind": true, "pattern": true},
}

// ParseLaw parses one law file. wantName is the file's stem: the two must
// agree, so the law's fixtures and baseline can be found by name alone.
func ParseLaw(text, wantName string) (Law, error) {
	doc, err := parseTOML(text)
	if err != nil {
		return Law{}, err
	}
	for _, section := range doc.sectionNames() {
		if section != "scope" && section != "matcher" {
			return Law{}, fmt.Errorf("unknown table [%s] — a law declares [scope] and [matcher] only", section)
		}
	}
	for _, k := range doc.keys("") {
		if !rootKeys[k] {
			return Law{}, fmt.Errorf("unknown key %q — a law's root keys are name, description, severity, escape, escape_lines, baseline, code_only", k)
		}
	}

	law := Law{EscapeLines: 2, Source: text}
	if law.Name, err = requiredString(doc, "", "name"); err != nil {
		return Law{}, err
	}
	if wantName != "" && law.Name != wantName {
		return Law{}, fmt.Errorf("name = %q but the file is %s.toml — a law is found by its file name", law.Name, wantName)
	}
	if law.Description, err = requiredString(doc, "", "description"); err != nil {
		return Law{}, err
	}
	sev, err := requiredString(doc, "", "severity")
	if err != nil {
		return Law{}, err
	}
	switch sev {
	case "deny":
		law.Severity = Deny
	case "warn":
		law.Severity = Warn
	default:
		return Law{}, fmt.Errorf("severity = %q — a law is %q or %q", sev, "deny", "warn")
	}
	if v, ok := doc.value("", "escape"); ok {
		if v.kind != tomlString {
			return Law{}, fmt.Errorf("escape is a string, got %s", v.kind)
		}
		law.Escape = v.s
	}
	if v, ok := doc.value("", "escape_lines"); ok {
		if v.kind != tomlInt || v.i < 0 {
			return Law{}, fmt.Errorf("escape_lines is a non-negative integer")
		}
		law.EscapeLines = v.i
	}
	if v, ok := doc.value("", "baseline"); ok {
		if v.kind != tomlString {
			return Law{}, fmt.Errorf("baseline is a path string, got %s", v.kind)
		}
		law.Baseline = v.s
	}
	if v, ok := doc.value("", "code_only"); ok {
		if v.kind != tomlBool {
			return Law{}, fmt.Errorf("code_only is a boolean, got %s", v.kind)
		}
		law.CodeOnly = v.b
	}
	if law.Scope, err = parseScope(doc); err != nil {
		return Law{}, err
	}
	if law.Matcher, err = parseMatcher(doc); err != nil {
		return Law{}, err
	}
	return law, nil
}

func parseScope(doc *tomlDoc) (Scope, error) {
	if !doc.has("scope") {
		return Scope{}, fmt.Errorf("missing [scope] — a law must say which files it judges")
	}
	var s Scope
	for _, k := range doc.keys("scope") {
		v, _ := doc.value("scope", k)
		if v.kind != tomlArray {
			return Scope{}, fmt.Errorf("scope.%s is an array of globs, got %s", k, v.kind)
		}
		switch k {
		case "include":
			s.Include = v.list
		case "exclude":
			s.Exclude = v.list
		default:
			return Scope{}, fmt.Errorf("unknown key scope.%s — [scope] takes include and exclude", k)
		}
	}
	if len(s.Include) == 0 {
		return Scope{}, fmt.Errorf("scope.include is required and must name at least one glob")
	}
	return s, nil
}

func parseMatcher(doc *tomlDoc) (Matcher, error) {
	if !doc.has("matcher") {
		return Matcher{}, fmt.Errorf("missing [matcher] — a law must state exactly one rule")
	}
	kind := MatcherKind(doc.str("matcher", "kind"))
	allowed, ok := matcherKeys[kind]
	if !ok {
		return Matcher{}, fmt.Errorf("unknown matcher kind %q — known kinds: %s", kind, knownKinds())
	}
	for _, k := range doc.keys("matcher") {
		if _, ok := allowed[k]; !ok {
			return Matcher{}, fmt.Errorf("unknown key matcher.%s for kind %q", k, kind)
		}
	}
	for k, required := range allowed {
		if !required {
			continue
		}
		if _, ok := doc.value("matcher", k); !ok {
			return Matcher{}, fmt.Errorf("matcher.%s is required for kind %q", k, kind)
		}
	}

	m := Matcher{Kind: kind, Key: KeyLineContent, Lines: 2}
	var err error
	get := func(key string) *regexp.Regexp {
		if err != nil {
			return nil
		}
		var re *regexp.Regexp
		re, err = compileField(doc, key)
		return re
	}
	switch kind {
	case KindLineCount:
		v, _ := doc.value("matcher", "max")
		if v.kind != tomlInt || v.i <= 0 {
			return Matcher{}, fmt.Errorf("matcher.max is a positive integer")
		}
		m.Max, m.Key = v.i, KeyFile
	case KindRegexAbsent:
		m.Pattern = get("pattern")
		if v, ok := doc.value("matcher", "key"); ok {
			switch KeyKind(v.s) {
			case KeyFile:
				m.Key = KeyFile
			case KeyLineContent, "file:line-content":
				m.Key = KeyLineContent
			default:
				return Matcher{}, fmt.Errorf("matcher.key = %q — a key is %q or %q", v.s, KeyFile, KeyLineContent)
			}
		}
	case KindRegexPresent, KindDocPathResolves:
		m.Pattern = get("pattern")
		m.Key = KeyFile
		if kind == KindDocPathResolves {
			m.Key = KeyLineContent
		}
	case KindMarkerWithinLines:
		m.Trigger, m.Marker = get("trigger"), get("marker")
		if v, ok := doc.value("matcher", "lines"); ok {
			if v.kind != tomlInt || v.i < 0 {
				return Matcher{}, fmt.Errorf("matcher.lines is a non-negative integer")
			}
			m.Lines = v.i
		}
	case KindRegistryBothWays:
		m.EntryPattern, m.UsePattern = get("entry_pattern"), get("use_pattern")
		m.RegistryFile = doc.str("matcher", "registry_file")
		m.Key = KeyLineContent
		if err == nil {
			for name, re := range map[string]*regexp.Regexp{"entry_pattern": m.EntryPattern, "use_pattern": m.UsePattern} {
				if re.NumSubexp() < 1 {
					return Matcher{}, fmt.Errorf("matcher.%s must capture the name in group 1", name)
				}
			}
		}
	}
	if err != nil {
		return Matcher{}, err
	}
	return m, nil
}

// compileField compiles one matcher regex, naming the key when it does not
// compile — a broken pattern is a defect in the law, not in the tree.
func compileField(doc *tomlDoc, key string) (*regexp.Regexp, error) {
	v, ok := doc.value("matcher", key)
	if !ok || v.kind != tomlString {
		return nil, fmt.Errorf("matcher.%s is a regex string", key)
	}
	re, err := regexp.Compile(v.s)
	if err != nil {
		return nil, fmt.Errorf("matcher.%s does not compile: %w", key, err)
	}
	return re, nil
}

func knownKinds() string {
	names := make([]string, 0, len(matcherKeys))
	for k := range matcherKeys {
		names = append(names, string(k))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func requiredString(doc *tomlDoc, section, key string) (string, error) {
	v, ok := doc.value(section, key)
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	if v.kind != tomlString || v.s == "" {
		return "", fmt.Errorf("%s is a non-empty string", key)
	}
	return v.s, nil
}
