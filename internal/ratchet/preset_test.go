package ratchet

import (
	"strings"
	"testing"
)

// sampleParams covers every placeholder name any embedded preset declares —
// TestEveryPreset_RendersIntoAValidLaw uses it to prove each one is a real,
// loadable law once a repo supplies values, not just text that happens to
// parse as TOML.
var sampleParams = map[string]string{
	"pattern":       "(TODO)\\(",
	"prefixes":      "BORLD",
	"registry_file": "docs/dev_instruments.md",
	"files":         `"crates/a/src/b.rs"`,
	"roots":         `"server"`,
	"forbidden":     `"testrig"`,
	"min_reachable": "5",
	"include":       `"**/*_test.go"`,
}

// TestEveryPreset_RendersIntoAValidLaw proves every embedded preset, once its
// `{{name}}` slots are filled, parses as a real law: the mechanism `ratchet
// init` and `ratchet check`'s drift comparison both depend on is exercised
// against the actual shipped content, not a hand-picked example.
func TestEveryPreset_RendersIntoAValidLaw(t *testing.T) {
	entries, err := ListPresets()
	if err != nil {
		t.Fatalf("ListPresets: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no presets embedded")
	}
	for _, e := range entries {
		t.Run(e.Group+"/"+e.Name, func(t *testing.T) {
			raw, err := LoadPresetText(e.Group, e.Name)
			if err != nil {
				t.Fatalf("LoadPresetText: %v", err)
			}
			rendered, missing := RenderPresetText(raw, sampleParams)
			if len(missing) > 0 {
				t.Fatalf("sampleParams is missing %v — a new preset needs its placeholder added there too", missing)
			}
			law, err := ParseLaw(rendered, e.Name)
			if err != nil {
				t.Fatalf("rendered preset does not parse as a law: %v\n%s", err, rendered)
			}
			if law.Name != e.Name {
				t.Errorf("name = %q, want %q", law.Name, e.Name)
			}
		})
	}
}

// TestListPresets_FindsTheKnownGroupsAndCapturesParams is a coverage floor: a
// preset silently dropped from the embed (a typo'd //go:embed pattern) is a
// law family nobody can init, which this fails loudly on.
func TestListPresets_FindsTheKnownGroupsAndCapturesParams(t *testing.T) {
	entries, err := ListPresets()
	if err != nil {
		t.Fatalf("ListPresets: %v", err)
	}
	groups := map[string]int{}
	byRef := map[string]PresetEntry{}
	for _, e := range entries {
		groups[e.Group]++
		byRef[e.Group+"/"+e.Name] = e
	}
	for _, g := range []string{"common", "rust", "go"} {
		if groups[g] == 0 {
			t.Errorf("no presets found in group %q", g)
		}
	}
	nanGuard, ok := byRef["rust/nan_guard"]
	if !ok {
		t.Fatal("rust/nan_guard not found")
	}
	if len(nanGuard.Params) != 0 {
		t.Errorf("nan_guard.Params = %v, want none", nanGuard.Params)
	}
	hygiene, ok := byRef["common/comment_hygiene"]
	if !ok {
		t.Fatal("common/comment_hygiene not found")
	}
	if len(hygiene.Params) != 1 || hygiene.Params[0] != "pattern" {
		t.Errorf("comment_hygiene.Params = %v, want [pattern]", hygiene.Params)
	}
}

// TestListPresets_ReturnsEntriesSortedByGroupThenName pins the doc comment's
// contract ("sorted by group then name") against the real embedded data: the
// CLI display (`aphrollo ratchet presets`) and the project's own determinism
// contract both depend on this order, and nothing else in this file checks
// it — TestListPresets_FindsTheKnownGroupsAndCapturesParams only checks
// membership.
func TestListPresets_ReturnsEntriesSortedByGroupThenName(t *testing.T) {
	entries, err := ListPresets()
	if err != nil {
		t.Fatalf("ListPresets: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("only %d presets embedded, too few to prove an order", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		prev, cur := entries[i-1], entries[i]
		if prev.Group > cur.Group {
			t.Fatalf("entry %d: group %q sorts after group %q", i, prev.Group, cur.Group)
		}
		if prev.Group == cur.Group && prev.Name > cur.Name {
			t.Fatalf("entry %d: within group %q, name %q sorts after name %q", i, prev.Group, prev.Name, cur.Name)
		}
	}
}

// TestRenderPresetText_ReportsEveryUnfilledSlot proves a caller can tell,
// before writing anything, exactly which params it still owes.
func TestRenderPresetText_ReportsEveryUnfilledSlot(t *testing.T) {
	raw := `pattern = "{{a}}"
other   = "{{b}}"
`
	rendered, missing := RenderPresetText(raw, map[string]string{"a": "x"})
	if !strings.Contains(rendered, `pattern = "x"`) {
		t.Errorf("rendered = %q, want a substituted", rendered)
	}
	if !strings.Contains(rendered, "{{b}}") {
		t.Errorf("rendered = %q, an unfilled slot must be left as-is", rendered)
	}
	if len(missing) != 1 || missing[0] != "b" {
		t.Errorf("missing = %v, want [b]", missing)
	}
}

// TestWithExtendsRoundTripsThroughLoadLawsWithNoDrift is the end-to-end
// proof `ratchet init` depends on: write a preset rendered with real params
// via WithExtends, load it back as an ordinary law, and confirm both that it
// scans correctly (the extends/[params] additions never interfere) and that
// presetDrift reports nothing — the freshly written file always starts in
// sync with the preset it came from.
func TestWithExtends_RoundTripsThroughLoadLawsWithNoDrift(t *testing.T) {
	raw, err := LoadPresetText("common", "comment_hygiene")
	if err != nil {
		t.Fatalf("LoadPresetText: %v", err)
	}
	params := map[string]string{"pattern": "FIXME\\("}
	rendered, missing := RenderPresetText(raw, params)
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	final := WithExtends(rendered, "common", "comment_hygiene", params, []string{"pattern"})

	dir := t.TempDir()
	writeLaw(t, dir, "comment_hygiene", final)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 1 {
		t.Fatalf("loaded %d laws, want 1", len(laws))
	}
	l := laws[0]
	if l.Extends != "preset:common/comment_hygiene" {
		t.Errorf("Extends = %q", l.Extends)
	}
	if l.Params["pattern"] != "FIXME\\(" {
		t.Errorf("Params[pattern] = %q", l.Params["pattern"])
	}
	if !l.Matcher.Pattern.MatchString("// FIXME( fix this") {
		t.Error("the written law's own matcher must use the substituted pattern")
	}
	note, err := presetDrift(l)
	if err != nil {
		t.Fatalf("presetDrift: %v", err)
	}
	if note != "" {
		t.Errorf("note = %q, want none — a freshly written law starts in sync", note)
	}
}

// TestParsePresetRef_RejectsAMalformedReference proves a bad `extends` string
// is caught with a message naming what it wanted, not a silent no-op.
func TestParsePresetRef_RejectsAMalformedReference(t *testing.T) {
	for _, bad := range []string{"", "rust/nan_guard", "preset:rust", "preset:/nan_guard", "preset:rust/"} {
		if _, _, err := ParsePresetRef(bad); err == nil {
			t.Errorf("ParsePresetRef(%q) = nil error, want a rejection", bad)
		}
	}
	group, name, err := ParsePresetRef("preset:rust/nan_guard")
	if err != nil || group != "rust" || name != "nan_guard" {
		t.Errorf("ParsePresetRef = %q, %q, %v", group, name, err)
	}
}

// TestPresets_TestRemovedRendersForGoAndRust proves the two language presets
// (fully concrete, no `{{name}}` slots of their own) render into a loadable
// symbol-removed law: the Go one scoped to `_test.go`, the Rust one scoped to
// `.rs` and excluding `target/`.
func TestPresets_TestRemovedRendersForGoAndRust(t *testing.T) {
	goRaw, err := LoadPresetText("go", "test_removed")
	if err != nil {
		t.Fatalf("LoadPresetText(go, test_removed): %v", err)
	}
	goLaw, err := ParseLaw(goRaw, "test_removed")
	if err != nil {
		t.Fatalf("go/test_removed does not parse as a law: %v\n%s", err, goRaw)
	}
	if goLaw.Matcher.Kind != KindSymbolRemoved {
		t.Errorf("go/test_removed kind = %q, want %q", goLaw.Matcher.Kind, KindSymbolRemoved)
	}
	if !goLaw.Scope.Matches("internal/x/a_test.go") {
		t.Error("go/test_removed scope must reach a _test.go file")
	}
	if goLaw.Scope.Matches("internal/x/a.go") {
		t.Error("go/test_removed scope must not reach a non-test .go file")
	}

	rustRaw, err := LoadPresetText("rust", "test_removed")
	if err != nil {
		t.Fatalf("LoadPresetText(rust, test_removed): %v", err)
	}
	rustLaw, err := ParseLaw(rustRaw, "test_removed")
	if err != nil {
		t.Fatalf("rust/test_removed does not parse as a law: %v\n%s", err, rustRaw)
	}
	if rustLaw.Matcher.Kind != KindSymbolRemoved {
		t.Errorf("rust/test_removed kind = %q, want %q", rustLaw.Matcher.Kind, KindSymbolRemoved)
	}
	if !rustLaw.Scope.Matches("crates/a/src/lib.rs") {
		t.Error("rust/test_removed scope must reach a .rs file")
	}
	if rustLaw.Scope.Matches("target/debug/lib.rs") {
		t.Error("rust/test_removed scope must exclude target/")
	}
}

// TestRustTestRemovedPattern_MatchesPlainTokioAndAsync proves the rendered
// Rust pattern captures the function name for both a plain `#[test]` and an
// async `#[tokio::test]`, and never matches an ordinary helper function.
func TestRustTestRemovedPattern_MatchesPlainTokioAndAsync(t *testing.T) {
	raw, err := LoadPresetText("rust", "test_removed")
	if err != nil {
		t.Fatalf("LoadPresetText(rust, test_removed): %v", err)
	}
	law, err := ParseLaw(raw, "test_removed")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	if m := law.Matcher.Pattern.FindStringSubmatch("#[test]\nfn it_works() {"); m == nil || m[1] != "it_works" {
		t.Errorf("plain #[test] match = %v, want capture \"it_works\"", m)
	}
	if m := law.Matcher.Pattern.FindStringSubmatch("#[tokio::test]\nasync fn runs() {"); m == nil || m[1] != "runs" {
		t.Errorf("#[tokio::test] async match = %v, want capture \"runs\"", m)
	}
	if m := law.Matcher.Pattern.FindStringSubmatch("fn helper() {"); m != nil {
		t.Errorf("plain fn helper() must not match, got %v", m)
	}
}

// TestGoTestRemovedPattern_IgnoresBenchmarksAndHelpers proves the rendered Go
// pattern captures only a Test-prefixed function, never a Benchmark or a
// lowercase helper that merely starts with "test".
func TestGoTestRemovedPattern_IgnoresBenchmarksAndHelpers(t *testing.T) {
	raw, err := LoadPresetText("go", "test_removed")
	if err != nil {
		t.Fatalf("LoadPresetText(go, test_removed): %v", err)
	}
	law, err := ParseLaw(raw, "test_removed")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	if m := law.Matcher.Pattern.FindStringSubmatch("func TestFoo(t *testing.T) {"); m == nil || m[1] != "TestFoo" {
		t.Errorf("TestFoo match = %v, want capture \"TestFoo\"", m)
	}
	if m := law.Matcher.Pattern.FindStringSubmatch("func BenchmarkFoo(b *testing.B) {"); m != nil {
		t.Errorf("BenchmarkFoo must not match, got %v", m)
	}
	if m := law.Matcher.Pattern.FindStringSubmatch("func testHelper() {"); m != nil {
		t.Errorf("testHelper must not match, got %v", m)
	}
}

// TestPresets_CommonTestRemovedDescriptionHasNoParamTokensAfterRender proves
// the common preset's description reads as prose once rendered, not a
// half-filled template: RenderPresetText substitutes every `{{name}}` slot
// across the WHOLE text, so a `{{pattern}}` token left in the description
// would echo back whatever regex the caller supplied, mid-sentence.
func TestPresets_CommonTestRemovedDescriptionHasNoParamTokensAfterRender(t *testing.T) {
	raw, err := LoadPresetText("common", "test_removed")
	if err != nil {
		t.Fatalf("LoadPresetText(common, test_removed): %v", err)
	}
	rendered, missing := RenderPresetText(raw, sampleParams)
	if len(missing) != 0 {
		t.Fatalf("RenderPresetText missing = %v, want none", missing)
	}
	law, err := ParseLaw(rendered, "test_removed")
	if err != nil {
		t.Fatalf("ParseLaw: %v\n%s", err, rendered)
	}
	if strings.Contains(law.Description, "{{") {
		t.Errorf("description = %q, want no leftover {{ template token", law.Description)
	}
	if strings.Contains(law.Description, sampleParams["pattern"]) {
		t.Errorf("description = %q, must not echo the substituted pattern %q", law.Description, sampleParams["pattern"])
	}
}

const extendingLaw = `
name = "nan-guard-local"
description = "should be ignored — a preset supplies the real one"
extends = "preset:rust/nan_guard"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[params]
`

// TestPresetDrift_IsSilentWhenTheMatcherStillMatches proves an ordinary law
// that extends a preset and never forked its matcher reports no drift.
func TestPresetDrift_IsSilentWhenTheMatcherStillMatches(t *testing.T) {
	// This law has no [matcher] table of its own, which the strict parser
	// still requires — so this test forks the matcher to be BYTE-IDENTICAL
	// to the preset's own, proving equal content reports no drift.
	law, err := ParseLaw(extendingLaw+`
[matcher]
kind    = "regex-absent"
pattern = "\.clamp\("
key     = "file:line-content-hash"
`, "nan-guard-local")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	note, err := presetDrift(law)
	if err != nil {
		t.Fatalf("presetDrift: %v", err)
	}
	if note != "" {
		t.Errorf("note = %q, want none — the matcher is identical to the preset", note)
	}
}

// TestPresetDrift_WarnsWhenTheMatcherDiverged is the RED case: a law that
// extends a preset but hand-forked its matcher must be flagged, by name.
func TestPresetDrift_WarnsWhenTheMatcherDiverged(t *testing.T) {
	law, err := ParseLaw(extendingLaw+`
[matcher]
kind    = "regex-absent"
pattern = "different-pattern"
key     = "file:line-content-hash"
`, "nan-guard-local")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	note, err := presetDrift(law)
	if err != nil {
		t.Fatalf("presetDrift: %v", err)
	}
	if !strings.Contains(note, "nan-guard-local") || !strings.Contains(note, "preset:rust/nan_guard") {
		t.Errorf("note = %q, must name the law and the preset", note)
	}
}

// TestRuleSemantics_ArrayElementsWithEmbeddedCommaDoNotCollideWithASplitList
// proves ["a,b"] and ["a","b"] fingerprint differently. canonicalTOMLValue
// used to join array elements with a bare comma, so a single element
// containing a comma was indistinguishable from two elements split at that
// comma — --adopt's changed-since-HEAD guard (and the commit-time baseline
// guard routed through the same function) would then call a real [scope]
// change "unchanged".
func TestRuleSemantics_ArrayElementsWithEmbeddedCommaDoNotCollideWithASplitList(t *testing.T) {
	oneElement := `
name     = "x"
severity = "deny"

[matcher]
kind    = "regex-absent"
pattern = "x"

[scope]
include = ["a,b"]
`
	twoElements := `
name     = "x"
severity = "deny"

[matcher]
kind    = "regex-absent"
pattern = "x"

[scope]
include = ["a", "b"]
`
	one, err := RuleSemantics(oneElement)
	if err != nil {
		t.Fatalf("RuleSemantics(oneElement): %v", err)
	}
	two, err := RuleSemantics(twoElements)
	if err != nil {
		t.Fatalf("RuleSemantics(twoElements): %v", err)
	}
	if one == two {
		t.Errorf(`RuleSemantics(["a,b"]) = RuleSemantics(["a","b"]) = %q, want them to differ`, one)
	}
}

// TestRuleSemantics_AStringValueCannotForgeASiblingFieldAcrossTheSectionJoin
// proves the same class of collision one level up: canonicalSection joined
// its `k=v` parts with a bare `;`, and canonicalTOMLValue's tomlString case
// is a raw passthrough, so an unescaped `;` and `=` inside a string VALUE
// could forge a following field's canonical text. Law A folds everything
// into one `alias` string; law B has that same text split across a real
// `alias` and a real `exclude` — today they fingerprint identically even
// though they judge the tree differently (B excludes "target", A excludes
// nothing).
func TestRuleSemantics_AStringValueCannotForgeASiblingFieldAcrossTheSectionJoin(t *testing.T) {
	lawA := `
name     = "a"
severity = "deny"

[matcher]
kind    = "regex-absent"
pattern = "x"

[scope]
alias = "X;exclude=6:target"
`
	lawB := `
name     = "a"
severity = "deny"

[matcher]
kind    = "regex-absent"
pattern = "x"

[scope]
alias   = "X"
exclude = ["target"]
`
	a, err := RuleSemantics(lawA)
	if err != nil {
		t.Fatalf("RuleSemantics(lawA): %v", err)
	}
	b, err := RuleSemantics(lawB)
	if err != nil {
		t.Fatalf("RuleSemantics(lawB): %v", err)
	}
	if a == b {
		t.Errorf("RuleSemantics(lawA) = RuleSemantics(lawB) = %q, want them to differ — lawA's alias value forges lawB's separate exclude field", a)
	}
}
