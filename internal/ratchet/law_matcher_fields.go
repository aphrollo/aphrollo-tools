package ratchet

import (
	"fmt"
	"regexp"
)

// matcherKeySpec is one key a matcher kind accepts, and whether it is
// required. A slice, in declaration order, rather than a map: the required-
// field check below walks this order, so a law missing several required
// keys always names the same one first, run to run.
type matcherKeySpec struct {
	name     string
	required bool
}

// matcherKeys is the exact key set each matcher kind accepts, in a fixed
// order. Strictness is the contract: an unknown key is a typo that would
// otherwise silently disable half a rule.
var matcherKeys = map[MatcherKind][]matcherKeySpec{
	KindLineCount:          {{"kind", true}, {"max", true}, {"count", false}, {"unit_split", false}},
	KindRegexAbsent:        {{"kind", true}, {"pattern", true}, {"key", false}, {"count", false}},
	KindRegexPresent:       {{"kind", true}, {"pattern", true}},
	KindPathRegexAbsent:    {{"kind", true}, {"pattern", true}},
	KindMarkerWithinLines:  {{"kind", true}, {"trigger", true}, {"marker", true}, {"lines", false}, {"contiguous", false}, {"direction", false}},
	KindRegistryBothWays:   {{"kind", true}, {"registry_file", true}, {"entry_pattern", true}, {"use_pattern", true}},
	KindDocPathResolves:    {{"kind", true}, {"pattern", true}},
	KindDepGraphForbids:    {{"kind", true}, {"roots", true}, {"forbidden", true}, {"edges", false}, {"min_reachable", false}},
	KindFileSetContainment: {{"kind", true}, {"superset_file", true}, {"subset_file", true}, {"capture", true}},
	KindJSONNumberCeiling:  {{"kind", true}, {"files", true}, {"path", true}, {"tolerance_pct", false}, {"enabled_env", false}},
	KindGoBenchCeiling:     {{"kind", true}, {"files", true}, {"tolerance_pct", false}, {"enabled_env", false}},
	KindSymbolRemoved:      {{"kind", true}, {"pattern", true}},
	KindCoChange:           {{"kind", true}},
	KindHunkRegex:          {{"kind", true}, {"removed", false}, {"added", false}, {"paired", false}, {"mode", false}, {"name_group", false}},
	KindGoDepGraphForbids:  {{"kind", true}, {"roots", true}, {"forbidden", true}, {"min_reachable", false}},
}

// setMinReachable validates and fills the vacuity floor shared by every
// dependency-graph matcher kind (cargo's and Go's): a walk that reached
// fewer packages than this is not a clean verdict, it is a walk that
// resolved nothing.
func setMinReachable(doc *tomlDoc, m *Matcher) error {
	v, ok := doc.value("matcher", "min_reachable")
	if !ok {
		return nil
	}
	if v.kind != tomlInt || v.i < 0 {
		return fmt.Errorf("matcher.min_reachable is a non-negative integer")
	}
	m.MinReachable = v.i
	return nil
}

// matcherKeyAllowed reports whether key is one of allowed, by name.
func matcherKeyAllowed(allowed []matcherKeySpec, key string) bool {
	for _, spec := range allowed {
		if spec.name == key {
			return true
		}
	}
	return false
}

// depGraphField is one (key, destination) pair for a dep-graph-forbids
// law's package-list fields, validated in this FIXED order — never a Go
// map, whose iteration order is randomized per range and would make which
// missing field gets reported nondeterministic across otherwise identical
// runs of the same input.
type depGraphField struct {
	key  string
	dest *[]string
}

// setDepGraphForbidsFields validates and fills m.Roots and m.Forbidden in
// that stated order, so a law missing both always names matcher.roots
// first, never matcher.forbidden on one run and matcher.roots on the next.
func setDepGraphForbidsFields(doc *tomlDoc, m *Matcher) error {
	for _, f := range []depGraphField{{"roots", &m.Roots}, {"forbidden", &m.Forbidden}} {
		v, _ := doc.value("matcher", f.key)
		// `roots = "*"` is every workspace package: a rule about what NO
		// package may reach should not have to list them.
		if f.key == "roots" && v.kind == tomlString && v.s == AllRoots {
			*f.dest = []string{AllRoots}
			continue
		}
		if v.kind != tomlArray || len(v.list) == 0 {
			return fmt.Errorf("matcher.%s is a non-empty array of package names", f.key)
		}
		*f.dest = v.list
	}
	return nil
}

// setCeilingCommonFields fills the two fields json-number-ceiling and
// go-bench-ceiling both accept: an optional comparison tolerance and an
// optional env var that arms the law (a bench figure nobody reads unless a
// deliberate run set it).
func setCeilingCommonFields(doc *tomlDoc, m *Matcher) error {
	if v, ok := doc.value("matcher", "tolerance_pct"); ok {
		if v.kind != tomlInt || v.i < 0 {
			return fmt.Errorf("matcher.tolerance_pct is a non-negative integer")
		}
		m.TolerancePct = v.i
	}
	if v, ok := doc.value("matcher", "enabled_env"); ok {
		if v.kind != tomlString || v.s == "" {
			return fmt.Errorf("matcher.enabled_env is a non-empty environment variable name")
		}
		m.EnabledEnv = v.s
	}
	return nil
}

// requireOneCaptureGroupSymbolRemoved is symbol-removed's own capture-group
// check: its `pattern` must capture exactly the symbol name, in exactly one
// group — that group is the identity every finding is keyed by, so a
// pattern with none (or several, an ambiguous identity) is rejected at
// load, named by the law that declared it.
func requireOneCaptureGroupSymbolRemoved(err error, lawName string, pattern *regexp.Regexp) error {
	if err != nil || pattern.NumSubexp() == 1 {
		return err
	}
	return fmt.Errorf("law %q: matcher.pattern must have exactly one capture group (the symbol name), got %d",
		lawName, pattern.NumSubexp())
}

// setHunkRegexFields validates and fills a hunk-regex law's fields. At least
// one of removed/added is required; name_group and paired are exclusive
// (name_group already answers a whole-file question, not a per-pair one);
// name_group and HunkDiffers both need a captured NAME/literal, so both
// require removed to carry exactly one capture group, and default added to
// the same pattern when the law left it unset.
func setHunkRegexFields(doc *tomlDoc, m *Matcher, lawName string) error {
	var err error
	compile := func(key string) *regexp.Regexp {
		if err != nil {
			return nil
		}
		v, ok := doc.value("matcher", key)
		if !ok {
			return nil
		}
		if v.kind != tomlString || v.s == "" {
			err = fmt.Errorf("matcher.%s is a non-empty regex string", key)
			return nil
		}
		var re *regexp.Regexp
		if re, err = regexp.Compile(v.s); err != nil {
			err = fmt.Errorf("matcher.%s does not compile: %w", key, err)
		}
		return re
	}
	m.Removed = compile("removed")
	m.Added = compile("added")
	if err != nil {
		return err
	}
	if v, ok := doc.value("matcher", "paired"); ok {
		if v.kind != tomlBool {
			return fmt.Errorf("matcher.paired is a boolean, got %s", v.kind)
		}
		m.Paired = v.b
	}
	if v, ok := doc.value("matcher", "name_group"); ok {
		if v.kind != tomlBool {
			return fmt.Errorf("matcher.name_group is a boolean, got %s", v.kind)
		}
		m.NameGroup = v.b
	}
	m.HunkMode = HunkForbid
	if v, ok := doc.value("matcher", "mode"); ok {
		switch HunkRegexMode(v.s) {
		case HunkForbid, HunkDiffers:
			m.HunkMode = HunkRegexMode(v.s)
		default:
			return fmt.Errorf("matcher.mode = %q — a mode is %q or %q", v.s, HunkForbid, HunkDiffers)
		}
	}
	if m.Removed == nil && m.Added == nil {
		return fmt.Errorf("law %q: matcher.removed and matcher.added are both absent — a hunk-regex law needs at least one", lawName)
	}
	if m.NameGroup && m.Paired {
		return fmt.Errorf("law %q: matcher.name_group and matcher.paired are exclusive", lawName)
	}
	if m.NameGroup || m.HunkMode == HunkDiffers {
		// At least one group, never "exactly one": a multi-language law
		// (Go, Rust, Python, JS in the same `removed` pattern) alternates
		// one branch per language, each capturing into its OWN group — the
		// same shape registry-both-ways' entry_pattern/use_pattern already
		// allow, resolved the same way (lastCapture: whichever branch
		// actually matched is the only one with a non-empty group).
		if m.Removed == nil || m.Removed.NumSubexp() < 1 {
			return fmt.Errorf("law %q: matcher.removed must capture at least one group (the name/literal), got %v", lawName, m.Removed)
		}
		if m.Added == nil {
			m.Added = m.Removed
		}
	}
	if m.HunkMode == HunkDiffers && !m.Paired {
		return fmt.Errorf("law %q: matcher.mode = %q requires matcher.paired = true", lawName, HunkDiffers)
	}
	return nil
}

// requireCaptureGroups checks entry_pattern before use_pattern — a fixed
// order, never a map — so a registry-both-ways law whose entry_pattern AND
// use_pattern both lack a capture group always names entry_pattern first.
func requireCaptureGroups(entry, use *regexp.Regexp) error {
	for _, p := range []struct {
		name string
		re   *regexp.Regexp
	}{{"entry_pattern", entry}, {"use_pattern", use}} {
		if p.re.NumSubexp() < 1 {
			return fmt.Errorf("matcher.%s must capture the name in group 1", p.name)
		}
	}
	return nil
}
