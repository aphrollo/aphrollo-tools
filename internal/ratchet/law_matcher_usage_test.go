package ratchet

import (
	"fmt"
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

// matcherUsageCategory tells apart two populations that share one symptom
// (no real law exercises this kind/field) but nothing else: one is debt
// with an owner, the other is a correct steady state that may last
// forever. Mixing them in one flat list is the failure mode this type
// exists to prevent — within a month nobody re-reads free-text reasons,
// and a bootstrap entry that has sat for six months looks exactly like one
// that will never move.
type matcherUsageCategory string

const (
	// matcherUsageBootstrap is DEBT: a real law is owed, tracked by Ref,
	// and the entry should disappear within a release or two. If it does
	// not, that is exactly the signal this check exists to raise.
	matcherUsageBootstrap matcherUsageCategory = "bootstrap"
	// matcherUsageUnusedHere is NOT debt: this repo's own law set has no
	// occasion for the kind or field (several are cargo/JSON-shaped
	// matchers a Go-only repo's dogfood laws never need, or a capability
	// aimed at a downstream CONSUMING repo — see json-number-ceiling and
	// dep-graph-ceiling's own #437/#480). It may sit here indefinitely
	// with nobody owing anything.
	matcherUsageUnusedHere matcherUsageCategory = "unused-here"
)

// matcherUsageAllowance is one matcher KIND, or one optional FIELD of a
// kind that does have a real law, this repo's own .ratchet/laws tree does
// not yet exercise. Field == "" names the whole kind (no law of that kind
// exists at all); a non-empty Field names one optional field of a kind
// that DOES have a real law, but no law of that kind ever sets. Required
// fields are never listed here: a law of a kind cannot load at all without
// them, so their use is implied by the kind's own use.
type matcherUsageAllowance struct {
	Kind     MatcherKind
	Field    string
	Category matcherUsageCategory
	// Ref names the issue or PR that OWES the real law — who was going to
	// do it. Required for Category == matcherUsageBootstrap: debt with no
	// named owner is debt that gets lost, which is the failure #501 itself
	// describes. Left empty for matcherUsageUnusedHere, which by
	// definition owes nobody anything.
	Ref string
	// Reason says WHY this entry is in the category it is in, read at
	// review — it is what tells "awaiting its bootstrap commit" apart from
	// "measured and declined" within the same category.
	Reason string
}

// matcherUsageAllowlist is the ONLY place a matcher kind or optional field
// may go unexercised by a real law — see .ratchet/README.md's "landing a
// new matcher field" section for the three-step bootstrap sequence a
// matcherUsageBootstrap entry tracks the middle of. It shrinks only:
// landing the real law and its tracked fixtures removes a bootstrap entry
// in the same commit, never edits it in place to widen it. An unused-here
// entry may be promoted to bootstrap (a real need showed up) or removed
// (a law landed after all), but never silently reclassified the other way
// to escape this check.
var matcherUsageAllowlist = []matcherUsageAllowance{
	// --- bootstrap: debt, with an owner, expected to shrink ---
	{
		Kind: KindIdentResolves, Category: matcherUsageBootstrap, Ref: "#324",
		Reason: "landed engine-only (#501's bootstrap gap): the installed aphrollo binary predates it, so a real law would reject every commit on this checkout until that binary is rebuilt post-merge",
	},
	{
		Kind: KindRegistryBothWays, Field: "entry_column", Category: matcherUsageBootstrap, Ref: "#492",
		Reason: "landed fixture-only; both registry-both-ways laws here (dev_instrument_registry, readme_verb_registry) still read the entry name from the whole line, not one `|`-delimited cell",
	},
	{
		Kind: KindHunkRegex, Field: "name_group", Category: matcherUsageBootstrap, Ref: "#318",
		Reason: "relaxed to \"at least one group\" for a multi-language test_removed law, but that law's own escape (a `Removes-test: <name>: <why>` commit-message trailer) has nowhere to read the real commit message from until precommit.go wires Options.CommitMessage — landing the law before that would make a legitimate removal unescapable",
	},
	// --- unused-here: a correct steady state, nobody owes this ---
	{
		Kind: KindRegexPresent, Category: matcherUsageUnusedHere,
		Reason: "\"every file in scope must contain a pattern\" (a seeded proptest's explicit seed, per its own docstring) has no analogue in this repo's Go tests; proved only by RunFixtures",
	},
	{
		Kind: KindPathRegexAbsent, Category: matcherUsageUnusedHere,
		Reason: "no law in this repo needs a bare path-shape ban (a filename carrying a plan-item stamp); proved only by RunFixtures",
	},
	{
		Kind: KindDepGraphForbids, Category: matcherUsageUnusedHere,
		Reason: "this repo's one dependency-direction rule is Go-specific (go_dep_graph_no_reach.toml, kind go-dep-graph-forbids); the cargo-flavored dep-graph-forbids has no consumer in a Go-only repo, proved only by RunFixtures",
	},
	{
		Kind: KindDepGraphCeiling, Category: matcherUsageUnusedHere,
		Reason: "the matcher (#437) and its baseline-guard fix (#480) are complete and correct; the consuming repo that asked for both was borld, not this one, and this repo's own dependency graph has no root anybody has measured a ceiling for",
	},
	{
		Kind: KindFileSetContainment, Category: matcherUsageUnusedHere,
		Reason: "this repo's two \"every X is registered in Y\" needs both fit registry-both-ways instead (see .ratchet/README.md); file-set-containment's split subset_capture/superset_capture (#492) has no consumer here",
	},
	{
		Kind: KindJSONNumberCeiling, Category: matcherUsageUnusedHere,
		Reason: "no law in this repo ceilings a number read out of generated JSON; proved only by RunFixtures",
	},
	{
		Kind: KindLineCount, Field: "count", Category: matcherUsageUnusedHere,
		Reason: "module_size.toml, the one line-count law here, accepts the LineCountText default; none opts into count = \"code\"",
	},
	{
		Kind: KindLineCount, Field: "unit_split", Category: matcherUsageUnusedHere,
		Reason: "module_size.toml never splits a file into a second counted unit",
	},
	{
		Kind: KindRegexAbsent, Field: "count", Category: matcherUsageUnusedHere,
		Reason: "every regex-absent law here accepts the CountLines default; none opts into count = \"matches\"",
	},
	{
		Kind: KindRegexNear, Field: "direction", Category: matcherUsageUnusedHere,
		Reason: "both regex-near laws (discarded_error_absence, discarded_error_success) accept the DirectionAbove default",
	},
	{
		Kind: KindGoBenchCeiling, Field: "tolerance_pct", Category: matcherUsageUnusedHere,
		Reason: "bench_ceiling.toml accepts the default tolerance; no bench law here widens it",
	},
	{
		Kind: KindGoBenchCeiling, Field: "enabled_env", Category: matcherUsageUnusedHere,
		Reason: "bench_ceiling.toml runs unconditionally; no bench law here gates itself behind an env switch",
	},
	{
		Kind: KindHunkRegex, Field: "added", Category: matcherUsageUnusedHere,
		Reason: "expectation_moved.toml judges only matcher.removed; no hunk-regex law here is ADDED-only (the \"an assertion weakened\" case, judged on the added side alone)",
	},
}

// TestMatcherUsage_EveryKindAndOptionalFieldHasARealLawOrAnAllowlistEntry
// enumerates the matcher kinds and optional fields the ENGINE accepts —
// matcherKeys, this package's own source of truth, never a hand-written
// list a later kind could silently miss — and cross-references every real
// law under this repo's .ratchet/laws against it. A kind or field neither
// used by a real law NOR named in matcherUsageAllowlist means a capability
// landed with no user in this repo and nothing tracking it (#501); an
// allow-list entry a real law now exercises means the gap closed and the
// entry is stale debt the allow-list must drop, never carry forward — the
// same monotone, only-ever-shrinks shape the baselines take.
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

	allowed := map[string]matcherUsageAllowance{}
	var bootstrap []string
	for _, a := range matcherUsageAllowlist {
		key := string(a.Kind)
		if a.Field != "" {
			key += ":" + a.Field
		}
		switch a.Category {
		case matcherUsageBootstrap:
			if a.Ref == "" {
				t.Fatalf("matcherUsageAllowlist entry %q is bootstrap debt with no Ref — debt with no named owner is debt that gets lost", key)
			}
			bootstrap = append(bootstrap, fmt.Sprintf("%s (%s)", key, a.Ref))
		case matcherUsageUnusedHere:
			if a.Ref != "" {
				t.Fatalf("matcherUsageAllowlist entry %q is unused-here but carries Ref %q — unused-here owes nobody anything; either drop the Ref or recategorize as bootstrap", key, a.Ref)
			}
		default:
			t.Fatalf("matcherUsageAllowlist entry %q has no valid Category (got %q, want %q or %q)", key, a.Category, matcherUsageBootstrap, matcherUsageUnusedHere)
		}
		if a.Reason == "" {
			t.Fatalf("matcherUsageAllowlist entry %q carries no reason", key)
		}
		if _, dup := allowed[key]; dup {
			t.Fatalf("matcherUsageAllowlist entry %q listed twice", key)
		}
		allowed[key] = a
	}
	sort.Strings(bootstrap)
	// Logged, never printed unconditionally: a passing run stays quiet
	// about the (much larger) unused-here population and surfaces only
	// the debt that is supposed to be shrinking — the useful sentence, not
	// sixteen entries with a reason nobody re-reads.
	t.Logf("%d kind(s)/field(s) awaiting their bootstrap law: %s", len(bootstrap), strings.Join(bootstrap, ", "))

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
		t.Errorf("matcher kind/field with no real law and no matcherUsageAllowlist entry: %s — either write the real law under .ratchet/laws, or add a matcherUsageAllowlist entry: matcherUsageBootstrap with the Ref that owes the law if one is coming, matcherUsageUnusedHere with a reason if this repo genuinely has no occasion for it", strings.Join(newlyUnused, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("matcherUsageAllowlist entry no longer unused, a real law now exercises it — remove it: %s", strings.Join(stale, ", "))
	}
}
