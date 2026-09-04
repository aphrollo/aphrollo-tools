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
