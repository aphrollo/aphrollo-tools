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
	"errors"
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
	// KindRegexNear: a `trigger` line is a hit only when a `context` pattern
	// co-occurs within N lines (a discarded error whose branch returns an
	// empty value as though absence were the answer). It is the COMPLEMENT
	// of marker-within-lines, not a variant of it: there, finding the marker
	// EXCUSES the trigger; here, finding the context is what MAKES it an
	// offence — a trigger alone must never be a hit.
	KindRegexNear MatcherKind = "regex-near"
	// KindMarkerInPackage: a `trigger` line is a hit only when NEITHER its own
	// file NOR any other file beside it (same directory, in the law's own
	// scope) contains `marker` ANYWHERE — the package-scoped complement of
	// marker-within-lines' line-windowed excuse. It exists for isolation that
	// is a fact about the PACKAGE, not the file: a `TestMain` that seeds
	// `t.Setenv` for the whole package lives in one sibling file, and a
	// file-at-a-time matcher reads every OTHER file in that package as
	// unisolated even though it is exactly as covered as the file that
	// declares it.
	KindMarkerInPackage MatcherKind = "marker-in-package"
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
	// KindDepGraphCeiling: a root may not reach MORE workspace packages than
	// its baseline, through normal dependency edges — the complement of
	// dep-graph-forbids, which answers "may it reach THIS one" and has
	// nothing to say about "how much may it reach at all". One hit per root,
	// weighted by the count of packages reached, ceilinged like any other
	// counted baseline (a login/signup crate whose `shared` surface is ten
	// wire types pulling in 17 unrelated workspace crates through one
	// unpriced edge).
	KindDepGraphCeiling MatcherKind = "dep-graph-ceiling"
	// KindFileSetContainment: every capture in one file must appear in another
	// (a stand-in may refuse MORE than the real query, never less).
	KindFileSetContainment MatcherKind = "file-set-containment"
	// KindJSONNumberCeiling: a number read out of generated JSON may not exceed
	// its baseline by more than a tolerance (a bench figure nobody reads).
	KindJSONNumberCeiling MatcherKind = "json-number-ceiling"
	// KindGoBenchCeiling: B/op and allocs/op, read per benchmark name out of a
	// `go test -bench -benchmem` text file, may each only fall — sec/op is
	// wall-clock noise this matcher never reads.
	KindGoBenchCeiling MatcherKind = "go-bench-ceiling"
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

// LineCountMode is what a line-count law counts a file's lines AS.
type LineCountMode string

const (
	// LineCountText counts every line — the original, still the default.
	LineCountText LineCountMode = "text"
	// LineCountCode excludes blank lines and comment-only lines, judged by
	// the FILE'S OWN comment syntax by extension (`//` and `/* */` for
	// .rs/.go/.ts, `#` for .py/.sh/.toml) — never the law's `comment_prefix`,
	// which is a single language a whole law scans, not a per-file fact.
	LineCountCode LineCountMode = "code"
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
	// Context (KindRegexNear only) is the co-occurring pattern that turns a
	// Trigger match into a hit — the complement of Marker, which excuses one.
	Context *regexp.Regexp
	Max     int
	Lines   int
	// LineMode (KindLineCount only) is "text" (every line, the default) or
	// "code" (blank and comment-only lines excluded, by the file's own syntax).
	LineMode LineCountMode
	// UnitSplit (KindLineCount only): a line matching this regex splits the
	// file into two units judged separately against the same Max — the line
	// itself opens the SECOND unit (`path#tests`), everything above it is the
	// first (`path`). A file with no match is one unit, unchanged.
	UnitSplit *regexp.Regexp
	// Contiguous is the marker-local spelling of the law-level flag; both mean
	// the comment run directly beside the trigger.
	Contiguous bool
	Direction  Direction
	// Registry fields (KindRegistryBothWays).
	RegistryFile string
	EntryPattern *regexp.Regexp
	UsePattern   *regexp.Regexp
	// Dependency-graph fields (KindDepGraphForbids, KindDepGraphCeiling).
	Roots     []string
	Forbidden []string
	Edges     string
	// Counts (KindDepGraphCeiling only) is "workspace" (default — only
	// reached packages that are themselves workspace members count toward
	// the ceiling) or "all" (every reached package counts, third-party
	// included).
	Counts string
	// MinReachable is the vacuity floor: a walk that reached fewer packages
	// than this is not a clean verdict, it is a walk that resolved nothing.
	MinReachable int
	// Containment fields (KindFileSetContainment). SubsetCapture and
	// SupersetCapture are the two extraction patterns, always both set
	// after parsing: `capture` in TOML is shorthand that fills both with
	// the same pattern for when subset and superset share one notation;
	// `subset_capture`/`superset_capture` are given together when they do
	// not (a Cargo.toml members line vs a markdown table cell).
	SupersetFile    string
	SubsetFile      string
	SubsetCapture   *regexp.Regexp
	SupersetCapture *regexp.Regexp
	// JSON-ceiling fields (KindJSONNumberCeiling).
	Files        string
	JSONPath     string
	TolerancePct int
	EnabledEnv   string
	// Hunk-regex fields (KindHunkRegex) — see wholetree_hunkregex.go.
	Removed   *regexp.Regexp
	Added     *regexp.Regexp
	Paired    bool
	HunkMode  HunkRegexMode
	NameGroup bool
}

// SchemaVersion is the law schema this binary understands. A law may declare
// `schema = N`; absent means 1, the schema every law was written against
// before the key existed.
const SchemaVersion = 1

// Law is one declared rule, loaded from `.ratchet/laws/<name>.toml`.
type Law struct {
	// Schema is the declared schema version (1 when the law omits the key).
	// Newer records that it exceeds SchemaVersion: the law is still judged by
	// the keys this binary knows, unknown keys are skipped rather than
	// rejected, and the run names it so the half-read rule is visible. A repo
	// upgrading its laws ahead of a box's binary must never wedge that box.
	Schema int
	Newer  bool
	// UnknownKind is the raw `[matcher].kind` string this binary's compiled
	// matcherKeys table does not recognize, "" for every ordinarily-parsed
	// law. Set only when ParseLaw deliberately stopped short of building a
	// Matcher: a repo's laws can move ahead of a box's aphrollo binary (a
	// lane lands a new matcher kind before every checkout on the box is
	// rebuilt from it), and a law naming a kind this binary has never heard
	// of must not reject every OTHER law's commit — it is skipped instead,
	// loudly, by whichever caller judges the whole set (see Check's
	// SkippedLaws). Every consumer must check this before treating Matcher
	// as meaningful: a zero Matcher looks structurally valid.
	UnknownKind string
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
	// Extends names the preset this law was copied from (`preset:<group>/<name>`
	// — see `internal/ratchet/presets`), purely for provenance and DRIFT
	// checking: the law is otherwise a normal, fully self-contained file, and
	// this never changes how it is scanned. `ratchet check` warns when the
	// [matcher] here no longer matches the preset rendered with Params.
	Extends string
	// Params is the [params] table: the values `ratchet init` substituted
	// into the preset's `{{name}}` slots when it wrote this file, kept so a
	// later `ratchet check` can re-render the same preset to detect drift.
	Params map[string]string
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
	sets, err := LoadScopeSets(root)
	if err != nil {
		return nil, err
	}
	if err := resolveScopeAliases(laws, sets); err != nil {
		return nil, err
	}
	sort.Slice(laws, func(i, j int) bool { return laws[i].Name < laws[j].Name })
	return laws, nil
}

var rootKeys = map[string]bool{
	"schema": true,
	"name":   true, "description": true, "severity": true, "escape": true,
	"escape_lines": true, "baseline": true, "code_only": true,
	"comment_prefix": true, "contiguous": true, "trigger_exclude": true,
	"extends": true,
}

// ParseLaw parses one law file. wantName is the file's stem: the two must
// agree, so the law's fixtures and baseline can be found by name alone.
func ParseLaw(text, wantName string) (Law, error) {
	doc, err := parseTOML(text)
	if err != nil {
		return Law{}, err
	}
	schema, newer, err := parseSchema(doc)
	if err != nil {
		return Law{}, err
	}
	if !newer {
		for _, section := range doc.sectionNames() {
			if section != "scope" && section != "matcher" && section != "params" {
				return Law{}, fmt.Errorf("unknown table [%s] — a law declares [scope], [matcher] and, when it extends a preset, [params]", section)
			}
		}
		for _, k := range doc.keys("") {
			if !rootKeys[k] {
				return Law{}, fmt.Errorf("unknown key %q — a law's root keys are schema, name, description, severity, escape, escape_lines, baseline, code_only, extends", k)
			}
		}
	}

	law := Law{EscapeLines: 2, Source: text, Schema: schema, Newer: newer}
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
	if v, ok := doc.value("", "extends"); ok {
		if v.kind != tomlString || !strings.HasPrefix(v.s, "preset:") || !strings.Contains(v.s, "/") {
			return Law{}, fmt.Errorf(`extends = %q — a preset reference is "preset:<group>/<name>"`, v.s)
		}
		law.Extends = v.s
	}
	if doc.has("params") {
		law.Params = map[string]string{}
		for _, k := range doc.keys("params") {
			v, _ := doc.value("params", k)
			if v.kind != tomlString {
				return Law{}, fmt.Errorf("params.%s is a string, got %s", k, v.kind)
			}
			law.Params[k] = v.s
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
	if law.Matcher, err = parseMatcher(doc, newer, law.Name); err != nil {
		// The rest of the file parsed fine, so the law is returned intact
		// with UnknownKind set rather than failed outright — a caller that
		// judges the whole set (Check, RunFixtures) is the one that decides
		// to skip it and report the skip; a bare parse never does.
		var unknown *UnknownMatcherKindError
		if errors.As(err, &unknown) {
			law.UnknownKind = string(unknown.Kind)
			return law, nil
		}
		return Law{}, err
	}
	if law.Matcher.Contiguous {
		law.Contiguous = true
	}
	return law, nil
}

// parseSchema reads the optional `schema = N` version stamp. Absent is
// SchemaVersion — every law written before the key existed. A value ABOVE
// SchemaVersion means the file came from a newer binary: newer=true switches
// the rest of parsing to lenient, so an unknown key is skipped instead of
// rejected. A non-integer or non-positive value is a broken law, not a
// future one, and is rejected either way.
func parseSchema(doc *tomlDoc) (schema int, newer bool, err error) {
	v, ok := doc.value("", "schema")
	if !ok {
		return SchemaVersion, false, nil
	}
	if v.kind != tomlInt || v.i < 1 {
		return 0, false, fmt.Errorf("schema is a positive integer version, got %s", v.kind)
	}
	return v.i, v.i > SchemaVersion, nil
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
