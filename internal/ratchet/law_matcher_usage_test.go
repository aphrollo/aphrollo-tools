package ratchet

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// matcherUsageRepoRoot walks up from the test's own working directory to the
// directory holding go.mod, so the test finds THIS repo's own .ratchet/laws
// regardless of which package directory `go test` runs it from.
func matcherUsageRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test's working directory")
		}
		dir = parent
	}
}

// matcherUsageAllowance is one matcher KIND, or one optional FIELD of a kind
// that does have a real law, this repo's own .ratchet/laws tree does not yet
// exercise — engine capability proved only through RunFixtures' synthetic
// trees, never through real law loading, baselines or escapes. Field == ""
// names the whole kind (no law of that kind exists at all); a non-empty
// Field names one optional field of a kind that DOES have a real law, but
// no law of that kind ever sets. Required fields are never listed here: a
// law of a kind cannot load at all without them, so their use is implied by
// the kind's own use.
type matcherUsageAllowance struct {
	Kind   MatcherKind
	Field  string
	Reason string
}

// matcherUsageAllowlist is the ONLY place a matcher kind or optional field
// may go unexercised by a real law — see .ratchet/README.md's "landing a
// new matcher field" section for the three-step bootstrap sequence this
// list tracks the middle of. It shrinks only: landing the real law and its
// tracked fixtures removes the entry in the same commit, never edits it in
// place to widen it.
var matcherUsageAllowlist = []matcherUsageAllowance{
	{Kind: KindIdentResolves, Reason: "landed engine-only (#324/#501): the installed aphrollo binary predates it, so a real law using it would reject every commit on this checkout until that binary is rebuilt post-merge; its real law and fixtures are the deferred next step"},
	{Kind: KindRegexPresent, Reason: "no law in this repo currently needs \"every file in scope must contain a pattern\" (a seeded proptest's explicit seed, in the docstring's own example) — this repo is Go, which has no proptest seed convention; proved only by RunFixtures"},
	{Kind: KindPathRegexAbsent, Reason: "no law in this repo currently needs a bare path-shape ban (a filename carrying a plan-item stamp); proved only by RunFixtures"},
	{Kind: KindDepGraphForbids, Reason: "this repo's one dependency-direction rule is Go-specific (go_dep_graph_no_reach.toml, kind go-dep-graph-forbids, which reuses this kind's required-field shape); the cargo-flavored dep-graph-forbids has no consumer in a Go-only repo, proved only by RunFixtures"},
	{Kind: KindDepGraphCeiling, Reason: "no law in this repo currently ceilings a root's reachable-package count; proved only by RunFixtures"},
	{Kind: KindFileSetContainment, Reason: "#492's split subset_capture/superset_capture landed fixture-only; no real law in this repo compares a superset/subset file pair yet"},
	{Kind: KindJSONNumberCeiling, Reason: "no law in this repo currently ceilings a number read out of generated JSON; proved only by RunFixtures"},
	{Kind: KindLineCount, Field: "count", Reason: "module_size.toml, the one line-count law here, accepts the LineCountText default; none opts into count = \"code\""},
	{Kind: KindLineCount, Field: "unit_split", Reason: "module_size.toml never splits a file into a second counted unit"},
	{Kind: KindRegexAbsent, Field: "count", Reason: "every regex-absent law here accepts the CountLines default; none opts into count = \"matches\""},
	{Kind: KindRegexNear, Field: "direction", Reason: "both regex-near laws (discarded_error_absence, discarded_error_success) accept the DirectionAbove default"},
	{Kind: KindRegistryBothWays, Field: "entry_column", Reason: "#492 landed the split registry-entry column addressing fixture-only; both registry-both-ways laws here (dev_instrument_registry, readme_verb_registry) read the entry name from the whole line, not one `|`-delimited cell"},
	{Kind: KindGoBenchCeiling, Field: "tolerance_pct", Reason: "bench_ceiling.toml accepts the default tolerance; no bench law here widens it"},
	{Kind: KindGoBenchCeiling, Field: "enabled_env", Reason: "bench_ceiling.toml runs unconditionally; no bench law here gates itself behind an env switch"},
	{Kind: KindHunkRegex, Field: "added", Reason: "expectation_moved.toml judges only matcher.removed; no hunk-regex law here is ADDED-only (the \"an assertion weakened\" case, judged on the added side alone)"},
	{Kind: KindHunkRegex, Field: "name_group", Reason: "abba95e's multi-group support landed fixture-only; expectation_moved.toml uses matcher.mode = \"differs\" with a single capture group instead"},
}

// TestMatcherUsage_EveryKindAndOptionalFieldHasARealLawOrAnAllowlistEntry
// enumerates the matcher kinds and optional fields the ENGINE accepts —
// matcherKeys, this package's own source of truth, never a hand-written
// list a later kind could silently miss — and cross-references every real
// law under this repo's .ratchet/laws against it. A kind or field neither
// used by a real law NOR named in matcherUsageAllowlist means a capability
// landed with no user in this repo and nothing tracking it (#501); an
// allow-list entry a real law now exercises means the bootstrap's last step
// landed and the entry is stale debt the allow-list must drop, never carry
// forward — the same monotone, only-ever-shrinks shape the baselines take.
func TestMatcherUsage_EveryKindAndOptionalFieldHasARealLawOrAnAllowlistEntry(t *testing.T) {
	root := matcherUsageRepoRoot(t)
	lawDir := filepath.Join(root, ".ratchet", "laws")
	entries, err := os.ReadDir(lawDir)
	if err != nil {
		t.Fatal(err)
	}

	usedKind := map[MatcherKind]bool{}
	usedField := map[MatcherKind]map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(lawDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := parseTOML(string(data))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		kind := MatcherKind(doc.str("matcher", "kind"))
		if kind == "" {
			t.Fatalf("%s: [matcher].kind is missing or not a string", e.Name())
		}
		usedKind[kind] = true
		if usedField[kind] == nil {
			usedField[kind] = map[string]bool{}
		}
		for _, k := range doc.keys("matcher") {
			usedField[kind][k] = true
		}
	}

	// The candidate set of things that could be unused comes straight out
	// of the engine's matcherKeys map — the same map parseMatcher itself
	// dispatches on — never a list this test maintains by hand.
	unused := map[string]bool{}
	for kind, specs := range matcherKeys {
		if !usedKind[kind] {
			unused[string(kind)] = true
			continue
		}
		for _, spec := range specs {
			if spec.required {
				continue
			}
			if !usedField[kind][spec.name] {
				unused[string(kind)+":"+spec.name] = true
			}
		}
	}

	allowed := map[string]string{}
	for _, a := range matcherUsageAllowlist {
		key := string(a.Kind)
		if a.Field != "" {
			key += ":" + a.Field
		}
		if a.Reason == "" {
			t.Fatalf("matcherUsageAllowlist entry %q carries no reason", key)
		}
		if _, dup := allowed[key]; dup {
			t.Fatalf("matcherUsageAllowlist entry %q listed twice", key)
		}
		allowed[key] = a.Reason
	}

	var newlyUnused, stale []string
	for k := range unused {
		if _, ok := allowed[k]; !ok {
			newlyUnused = append(newlyUnused, k)
		}
	}
	for k := range allowed {
		if !unused[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(newlyUnused)
	sort.Strings(stale)

	if len(newlyUnused) > 0 {
		t.Errorf("matcher kind/field with no real law and no matcherUsageAllowlist entry: %s — add one with a reason, or land the real law instead", strings.Join(newlyUnused, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("matcherUsageAllowlist entry no longer unused, a real law now exercises it — remove it: %s", strings.Join(stale, ", "))
	}
}
