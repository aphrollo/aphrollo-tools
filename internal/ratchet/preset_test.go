package ratchet

import (
	"strings"
	"testing"
)

// sampleParams covers every placeholder name any embedded preset declares —
// TestEveryPresetRendersIntoAValidLaw uses it to prove each one is a real,
// loadable law once a repo supplies values, not just text that happens to
// parse as TOML.
var sampleParams = map[string]string{
	"pattern":       "TODO\\(",
	"prefixes":      "BORLD",
	"registry_file": "docs/dev_instruments.md",
	"files":         `"crates/a/src/b.rs"`,
	"roots":         `"server"`,
	"forbidden":     `"testrig"`,
	"min_reachable": "5",
}

// TestEveryPresetRendersIntoAValidLaw proves every embedded preset, once its
// `{{name}}` slots are filled, parses as a real law: the mechanism `ratchet
// init` and `ratchet check`'s drift comparison both depend on is exercised
// against the actual shipped content, not a hand-picked example.
func TestEveryPresetRendersIntoAValidLaw(t *testing.T) {
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

// TestListPresetsFindsTheKnownGroupsAndCapturesParams is a coverage floor: a
// preset silently dropped from the embed (a typo'd //go:embed pattern) is a
// law family nobody can init, which this fails loudly on.
func TestListPresetsFindsTheKnownGroupsAndCapturesParams(t *testing.T) {
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

// TestRenderPresetTextReportsEveryUnfilledSlot proves a caller can tell,
// before writing anything, exactly which params it still owes.
func TestRenderPresetTextReportsEveryUnfilledSlot(t *testing.T) {
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
func TestWithExtendsRoundTripsThroughLoadLawsWithNoDrift(t *testing.T) {
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

// TestParsePresetRefRejectsAMalformedReference proves a bad `extends` string
// is caught with a message naming what it wanted, not a silent no-op.
func TestParsePresetRefRejectsAMalformedReference(t *testing.T) {
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

const extendingLaw = `
name = "nan-guard-local"
description = "should be ignored — a preset supplies the real one"
extends = "preset:rust/nan_guard"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[params]
`

// TestPresetDriftIsSilentWhenTheMatcherStillMatches proves an ordinary law
// that extends a preset and never forked its matcher reports no drift.
func TestPresetDriftIsSilentWhenTheMatcherStillMatches(t *testing.T) {
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

// TestPresetDriftWarnsWhenTheMatcherDiverged is the RED case: a law that
// extends a preset but hand-forked its matcher must be flagged, by name.
func TestPresetDriftWarnsWhenTheMatcherDiverged(t *testing.T) {
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
