package ratchet

import (
	"slices"
	"strings"
	"testing"
)

// A `{{name|default}}` slot nobody supplied renders its default and is not
// owed: a preset can offer a param a repo may set without making every repo
// set it.
func TestRenderPresetText_FillsAnUnsuppliedSlotFromItsDefault(t *testing.T) {
	raw := `include = [{{src|"**/*.rs", "**/*.go"}}]
pattern = "{{pattern}}"
`
	rendered, missing := RenderPresetText(raw, map[string]string{"pattern": "x"})
	want := `include = ["**/*.rs", "**/*.go"]
pattern = "x"
`
	if rendered != want {
		t.Errorf("rendered = %q, want %q", rendered, want)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none: a defaulted slot is never owed", missing)
	}
}

// A supplied value wins over the default, at every occurrence of the slot.
func TestRenderPresetText_ASuppliedValueReplacesTheDefault(t *testing.T) {
	raw := `a = [{{src|"**/*.rs"}}]
b = [{{src|"**/*.rs"}}]
`
	rendered, missing := RenderPresetText(raw, map[string]string{"src": `"**/*.py"`})
	if want := "a = [\"**/*.py\"]\nb = [\"**/*.py\"]\n"; rendered != want {
		t.Errorf("rendered = %q, want %q", rendered, want)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}
}

// A slot left unfilled is owed once, however often it appears.
func TestRenderPresetText_NamesARepeatedUnfilledSlotOnce(t *testing.T) {
	rendered, missing := RenderPresetText("a = \"{{b}}\"\nc = \"{{b}}\"\n", nil)
	if !slices.Equal(missing, []string{"b"}) {
		t.Errorf("missing = %v, want [b]", missing)
	}
	if strings.Count(rendered, "{{b}}") != 2 {
		t.Errorf("rendered = %q, an unfilled slot must be left as-is", rendered)
	}
}

// The params a preset lists are slot NAMES, a defaulted one included, and its
// default is carried alongside.
func TestListPresets_ListsADefaultedSlotByNameWithItsDefault(t *testing.T) {
	entries, err := ListPresets()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Group+"/"+e.Name != "common/comment_hygiene" {
			continue
		}
		// pattern comes first in the file and has no default; source_include
		// comes after it and has one, so every slot is read, not only the first.
		if _, ok := e.Defaults["pattern"]; ok {
			t.Errorf("comment_hygiene's pattern has no default, yet Defaults carries one: %v", e.Defaults)
		}
		if got := e.Defaults["source_include"]; got != `"**/*.rs", "**/*.go", "**/*.py", "**/*.ts"` {
			t.Errorf("comment_hygiene Defaults[source_include] = %q", got)
		}
	}
	for _, e := range entries {
		if e.Group+"/"+e.Name != "common/module_size" {
			continue
		}
		if !slices.Equal(e.Params, []string{"source_include"}) {
			t.Errorf("Params = %v, want [source_include]", e.Params)
		}
		if got := e.Defaults["source_include"]; got != `"**/*.rs", "**/*.go"` {
			t.Errorf("Defaults[source_include] = %q", got)
		}
		return
	}
	t.Fatal("common/module_size not found")
}

// Every common preset that judges source code takes its source globs from
// source_include: left alone it keeps the scope it has always had, and a
// repo in another language sets it. Globs a preset needs beyond source code
// (the markdown a doc rule reads) stay whatever source_include says.
func TestCommonPresets_SourceIncludeRescopesAndDefaultsToTodaysScope(t *testing.T) {
	cases := []struct {
		name         string
		defaultScope []string
		rescoped     []string
	}{
		{"comment_hygiene", []string{"**/*.rs", "**/*.go", "**/*.py", "**/*.ts"}, []string{"**/*.py", "**/*.svelte"}},
		{"identifier_history", []string{"**/*.rs", "**/*.go", "**/*.py", "**/*.ts"}, []string{"**/*.py", "**/*.svelte"}},
		{"module_size", []string{"**/*.rs", "**/*.go"}, []string{"**/*.py", "**/*.svelte"}},
		{"path_literals", []string{"**/*.go", "**/*.rs", "**/*.py"}, []string{"**/*.py", "**/*.svelte"}},
		{"transient_doc_reference", []string{"**/*.md", "**/*.rs", "**/*.go"}, []string{"**/*.md", "**/*.py", "**/*.svelte"}},
		{"dev_instrument_registry", []string{"**/*.rs", "**/*.go", "**/*.md"}, []string{"**/*.py", "**/*.svelte", "**/*.md"}},
	}
	params := map[string]string{"pattern": "FIXME", "prefixes": "APP"}
	rescope := map[string]string{"pattern": "FIXME", "prefixes": "APP", "source_include": `"**/*.py", "**/*.svelte"`}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := LoadPresetText("common", c.name)
			if err != nil {
				t.Fatal(err)
			}
			for _, run := range []struct {
				params map[string]string
				want   []string
			}{{params, c.defaultScope}, {rescope, c.rescoped}} {
				rendered, missing := RenderPresetText(raw, run.params)
				if len(missing) != 0 {
					t.Fatalf("missing = %v", missing)
				}
				law, err := ParseLaw(rendered, c.name)
				if err != nil {
					t.Fatalf("ParseLaw: %v\n%s", err, rendered)
				}
				if !slices.Equal(law.Scope.Include, run.want) {
					t.Errorf("scope include = %v, want %v", law.Scope.Include, run.want)
				}
			}
		})
	}
}

// A written law records only the params a repo supplied: a defaulted slot
// left alone has no value to record, and an empty one would read as a
// deliberate choice.
func TestWithExtends_RecordsOnlySuppliedParams(t *testing.T) {
	got := WithExtends("name = \"x\"\n", "common", "x", map[string]string{"pattern": "FIXME"}, []string{"source_include", "pattern"})
	want := "name = \"x\"\nextends     = \"preset:common/x\"\n\n[params]\npattern = \"FIXME\"\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// With nothing supplied at all there is no [params] table.
func TestWithExtends_WritesNoParamsTableWhenNoneWasSupplied(t *testing.T) {
	got := WithExtends("name = \"x\"\n", "common", "x", nil, []string{"source_include"})
	if want := "name = \"x\"\nextends     = \"preset:common/x\"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
