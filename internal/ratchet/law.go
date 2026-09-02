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
	// KindPathRegexAbsent: a repo-relative PATH must not match a pattern (a
	// filename carrying a plan-item stamp or a serial letter).
	KindPathRegexAbsent MatcherKind = "path-regex-absent"
	// KindDepGraphForbids: a production root may not REACH a forbidden package
	// through normal dependency edges (dev-only tooling in a shipping binary).
	KindDepGraphForbids MatcherKind = "dep-graph-forbids"
	// KindFileSetContainment: every capture in one file must appear in another
	// (a stand-in may refuse MORE than the real query, never less).
	KindFileSetContainment MatcherKind = "file-set-containment"
	// KindJSONNumberCeiling: a number read out of generated JSON may not exceed
	// its baseline by more than a tolerance (a bench figure nobody reads).
	KindJSONNumberCeiling MatcherKind = "json-number-ceiling"
)

// AllRoots is `roots = "*"`: every package in the workspace is a root, which
// is what a rule like "no package may reach the scratch crate" states.
const AllRoots = "*"

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

// CountKind is what a regex-absent law counts.
type CountKind string

const (
	// CountLines counts one offence per offending LINE.
	CountLines CountKind = "lines"
	// CountMatches counts every match on a line: a law about calls wants the
	// call count, and three on one line is three offences.
	CountMatches CountKind = "matches"
)

// Direction is where a marker-within-lines law looks for its marker. Above is
// the comment-above-the-declaration shape; below is the block that carries its
// own configuration (a `proptest!` block's `#![proptest_config(…)]` is on the
// NEXT line, and looking up only reports every seeded block as unseeded).
type Direction string

const (
	DirectionAbove Direction = "above"
	DirectionBelow Direction = "below"
	DirectionBoth  Direction = "both"
)

// Matcher is a law's one rule.
type Matcher struct {
	Kind    MatcherKind
	Key     KeyKind
	Count   CountKind
	Pattern *regexp.Regexp
	Trigger *regexp.Regexp
	Marker  *regexp.Regexp
	Max     int
	Lines   int
	// Contiguous is the marker-local spelling of the law-level flag; both mean
	// the comment run directly beside the trigger.
	Contiguous bool
	Direction  Direction
	// Registry fields (KindRegistryBothWays).
	RegistryFile string
	EntryPattern *regexp.Regexp
	UsePattern   *regexp.Regexp
	// Dependency-graph fields (KindDepGraphForbids).
	Roots     []string
	Forbidden []string
	Edges     string
	// MinReachable is the vacuity floor: a walk that reached fewer packages
	// than this is not a clean verdict, it is a walk that resolved nothing.
	MinReachable int
	// Containment fields (KindFileSetContainment).
	SupersetFile string
	SubsetFile   string
	Capture      *regexp.Regexp
	// JSON-ceiling fields (KindJSONNumberCeiling).
	Files        string
	JSONPath     string
	TolerancePct int
	EnabledEnv   string
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
	// CommentPrefix opens a comment in the language the law scans (`//` by
	// default, `#` for TOML/shell), deciding what CodeOnly strips and what
	// counts as a comment line in a contiguous run.
	CommentPrefix string
	// Contiguous binds an escape or a marker to the COMMENT RUN directly above
	// the trigger: any code or blank line between breaks it. Counting lines
	// instead lets one comment exempt an unrelated call below it.
	Contiguous bool
	// TriggerExclude names lines that can never be a trigger (an import naming
	// the very type the law is about).
	TriggerExclude *regexp.Regexp
	Matcher        Matcher
	// Path is the law file itself, so an error can name where the rule came from.
	Path string
	// Root is the tree the law is judged against; a doc-path-resolves law
	// resolves its citations relative to it.
	Root string
	// Source is the law file's text, hashed into the scan cache key: a rule
	// that changed must never be answered from a cache filled under the old one.
	Source string
	// CacheDir is where a law expensive enough to cache keeps its verdict;
	// empty means recompute every run.
	CacheDir string
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
		law.Path, law.Root = path, root
		laws = append(laws, law)
	}
	sort.Slice(laws, func(i, j int) bool { return laws[i].Name < laws[j].Name })
	return laws, nil
}

var rootKeys = map[string]bool{
	"name": true, "description": true, "severity": true, "escape": true,
	"escape_lines": true, "baseline": true, "code_only": true,
	"comment_prefix": true, "contiguous": true, "trigger_exclude": true,
}

// matcherKeys is the exact key set each matcher kind accepts, and whether each
// is required. Strictness is the contract: an unknown key is a typo that would
// otherwise silently disable half a rule.
var matcherKeys = map[MatcherKind]map[string]bool{
	KindLineCount:          {"kind": true, "max": true},
	KindRegexAbsent:        {"kind": true, "pattern": true, "key": false, "count": false},
	KindRegexPresent:       {"kind": true, "pattern": true},
	KindPathRegexAbsent:    {"kind": true, "pattern": true},
	KindMarkerWithinLines:  {"kind": true, "trigger": true, "marker": true, "lines": false, "contiguous": false, "direction": false},
	KindRegistryBothWays:   {"kind": true, "registry_file": true, "entry_pattern": true, "use_pattern": true},
	KindDocPathResolves:    {"kind": true, "pattern": true},
	KindDepGraphForbids:    {"kind": true, "roots": true, "forbidden": true, "edges": false, "min_reachable": false},
	KindFileSetContainment: {"kind": true, "superset_file": true, "subset_file": true, "capture": true},
	KindJSONNumberCeiling:  {"kind": true, "files": true, "path": true, "tolerance_pct": false, "enabled_env": false},
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
	if v, ok := doc.value("", "comment_prefix"); ok {
		if v.kind != tomlString || v.s == "" {
			return Law{}, fmt.Errorf("comment_prefix is a non-empty string, got %s", v.kind)
		}
		law.CommentPrefix = v.s
	}
	if v, ok := doc.value("", "contiguous"); ok {
		if v.kind != tomlBool {
			return Law{}, fmt.Errorf("contiguous is a boolean, got %s", v.kind)
		}
		law.Contiguous = v.b
	}
	if v, ok := doc.value("", "trigger_exclude"); ok {
		if v.kind != tomlString || v.s == "" {
			return Law{}, fmt.Errorf("trigger_exclude is a regex string, got %s", v.kind)
		}
		if law.TriggerExclude, err = regexp.Compile(v.s); err != nil {
			return Law{}, fmt.Errorf("trigger_exclude does not compile: %w", err)
		}
	}
	if law.Contiguous {
		if _, ok := doc.value("", "escape_lines"); ok {
			return Law{}, fmt.Errorf("contiguous and escape_lines say different things — the run above the trigger IS the window")
		}
	}
	if law.Scope, err = parseScope(doc); err != nil {
		return Law{}, err
	}
	if law.Matcher, err = parseMatcher(doc); err != nil {
		return Law{}, err
	}
	if law.Matcher.Contiguous {
		law.Contiguous = true
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
		if v.kind != tomlArray {
			return Scope{}, fmt.Errorf("scope.%s is an array of globs, got %s", k, v.kind)
		}
		switch k {
		case "include":
			s.Include = v.list
		case "exclude":
			s.Exclude = v.list
		default:
			return Scope{}, fmt.Errorf("unknown key scope.%s — [scope] takes include, exclude, ignore_gitignore and min_files", k)
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

	m := Matcher{Kind: kind, Key: KeyLineContent, Lines: 2, Count: CountLines, Direction: DirectionAbove}
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
		if v, ok := doc.value("matcher", "count"); ok {
			switch CountKind(v.s) {
			case CountLines, CountMatches:
				m.Count = CountKind(v.s)
			default:
				return Matcher{}, fmt.Errorf("matcher.count = %q — a count is %q or %q", v.s, CountLines, CountMatches)
			}
		}
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
	case KindPathRegexAbsent:
		m.Pattern = get("pattern")
		m.Key = KeyFile
	case KindRegexPresent, KindDocPathResolves:
		m.Pattern = get("pattern")
		m.Key = KeyFile
		if kind == KindDocPathResolves {
			m.Key = KeyLineContent
		}
	case KindMarkerWithinLines:
		m.Trigger, m.Marker = get("trigger"), get("marker")
		if v, ok := doc.value("matcher", "direction"); ok {
			switch Direction(v.s) {
			case DirectionAbove, DirectionBelow, DirectionBoth:
				m.Direction = Direction(v.s)
			default:
				return Matcher{}, fmt.Errorf("matcher.direction = %q — a direction is %q, %q or %q", v.s, DirectionAbove, DirectionBelow, DirectionBoth)
			}
		}
		if v, ok := doc.value("matcher", "contiguous"); ok {
			if v.kind != tomlBool {
				return Matcher{}, fmt.Errorf("matcher.contiguous is a boolean, got %s", v.kind)
			}
			m.Contiguous = v.b
			if _, both := doc.value("matcher", "lines"); both && v.b {
				return Matcher{}, fmt.Errorf("matcher.contiguous and matcher.lines say different things — the comment run above the trigger IS the window")
			}
		}
		if v, ok := doc.value("matcher", "lines"); ok {
			if v.kind != tomlInt || v.i < 0 {
				return Matcher{}, fmt.Errorf("matcher.lines is a non-negative integer")
			}
			m.Lines = v.i
		}
	case KindDepGraphForbids:
		m.Key = KeyLineContent
		m.Edges = "normal"
		if v, ok := doc.value("matcher", "edges"); ok {
			if v.s != "normal" && v.s != "all" {
				return Matcher{}, fmt.Errorf("matcher.edges is %q or %q, got %q", "normal", "all", v.s)
			}
			m.Edges = v.s
		}
		if v, ok := doc.value("matcher", "min_reachable"); ok {
			if v.kind != tomlInt || v.i < 0 {
				return Matcher{}, fmt.Errorf("matcher.min_reachable is a non-negative integer")
			}
			m.MinReachable = v.i
		}
		for key, dest := range map[string]*[]string{"roots": &m.Roots, "forbidden": &m.Forbidden} {
			v, _ := doc.value("matcher", key)
			// `roots = "*"` is every workspace package: a rule about what NO
			// package may reach should not have to list them.
			if key == "roots" && v.kind == tomlString && v.s == AllRoots {
				*dest = []string{AllRoots}
				continue
			}
			if v.kind != tomlArray || len(v.list) == 0 {
				return Matcher{}, fmt.Errorf("matcher.%s is a non-empty array of package names", key)
			}
			*dest = v.list
		}
	case KindFileSetContainment:
		m.Key = KeyLineContent
		m.Capture = get("capture")
		m.SupersetFile = doc.str("matcher", "superset_file")
		m.SubsetFile = doc.str("matcher", "subset_file")
		if err == nil && m.Capture.NumSubexp() < 1 {
			return Matcher{}, fmt.Errorf("matcher.capture must capture the name in group 1")
		}
	case KindJSONNumberCeiling:
		m.Key = KeyFile
		m.Files = doc.str("matcher", "files")
		m.JSONPath = doc.str("matcher", "path")
		if v, ok := doc.value("matcher", "tolerance_pct"); ok {
			if v.kind != tomlInt || v.i < 0 {
				return Matcher{}, fmt.Errorf("matcher.tolerance_pct is a non-negative integer")
			}
			m.TolerancePct = v.i
		}
		if v, ok := doc.value("matcher", "enabled_env"); ok {
			if v.kind != tomlString || v.s == "" {
				return Matcher{}, fmt.Errorf("matcher.enabled_env is a non-empty environment variable name")
			}
			m.EnabledEnv = v.s
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
