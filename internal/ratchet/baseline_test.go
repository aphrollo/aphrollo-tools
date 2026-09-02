package ratchet

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBaselineParseRenderIsByteIdentical(t *testing.T) {
	for _, c := range []struct {
		name string
		form Form
		text string
	}{
		{"counted", Counted, "# header\n# more\ncrates/a.rs | 900\ncrates/b.rs | 620\n"},
		{"multiset", Multiset, "# header\ncrates/a.rs | x.clamp(0.0, 1.0)\ncrates/a.rs | x.clamp(0.0, 1.0)\n"},
	} {
		b, err := ParseBaseline(c.text, c.form)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := b.Render(); got != c.text {
			t.Errorf("%s: render = %q, want %q", c.name, got, c.text)
		}
	}
}

func TestBaselineTightenLowersRemovesAndNeverRaisesOrAdds(t *testing.T) {
	b, err := ParseBaseline("crates/a.rs | 900\ncrates/b.rs | 620\ncrates/c.rs | 700\n", Counted)
	if err != nil {
		t.Fatal(err)
	}
	tt := b.Tighten(map[string]int{
		"crates/a.rs":   650, // shrank: lower it
		"crates/c.rs":   999, // grew: a regression, never a raise
		"crates/new.rs": 5,   // brand new: never added by tighten
	})
	if !reflect.DeepEqual(tt.Lowered, []Change{{"crates/a.rs", 900, 650}}) {
		t.Errorf("lowered = %+v", tt.Lowered)
	}
	if !reflect.DeepEqual(tt.Removed, []Change{{"crates/b.rs", 620, 0}}) {
		t.Errorf("removed = %+v", tt.Removed)
	}
	if got, want := b.Render(), "crates/a.rs | 650\ncrates/c.rs | 700\n"; got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

func TestBaselineRegressionsReportsGrownAndBrandNewKeys(t *testing.T) {
	b, _ := ParseBaseline("crates/a.rs | 600\n", Counted)
	got := b.Regressions(map[string]int{"crates/a.rs": 650, "crates/new.rs": 1})
	want := []Regression{
		{Key: "crates/a.rs", Baseline: 600, Measured: 650},
		{Key: "crates/new.rs", Baseline: 0, Measured: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("regressions = %+v, want %+v", got, want)
	}
	if r := b.Regressions(map[string]int{"crates/a.rs": 600}); len(r) != 0 {
		t.Errorf("at-ceiling must not regress: %+v", r)
	}
}

// The multiset form is what makes swapping one offending site for another in
// the same file visible: the totals are equal, the identities are not.
func TestBaselineMultisetSeesASwappedSite(t *testing.T) {
	b, _ := ParseBaseline("crates/a.rs | x.sin()\ncrates/a.rs | y.cos()\n", Multiset)
	measured := map[string]int{"crates/a.rs | x.sin()": 2}
	got := b.Regressions(measured)
	if len(got) != 1 || got[0].Key != "crates/a.rs | x.sin()" {
		t.Fatalf("regressions = %+v", got)
	}
	tt := b.Tighten(measured)
	if !reflect.DeepEqual(tt.Removed, []Change{{"crates/a.rs | y.cos()", 1, 0}}) {
		t.Errorf("removed = %+v", tt.Removed)
	}
}

func TestBaselineMultisetDropsOnlyTheExcessDuplicateLines(t *testing.T) {
	text := "crates/a.rs | v.clamp(0.0, 1.0)\ncrates/a.rs | v.clamp(0.0, 1.0)\n"
	b, _ := ParseBaseline(text, Multiset)
	b.Tighten(map[string]int{"crates/a.rs | v.clamp(0.0, 1.0)": 1})
	if got, want := b.Render(), "crates/a.rs | v.clamp(0.0, 1.0)\n"; got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

func TestBaselineMidFileCommentsSurviveATightenUntouched(t *testing.T) {
	text := "# header\ncrates/a.rs | 900\n# mid-file note\n\ncrates/b.rs | 620\n"
	b, _ := ParseBaseline(text, Counted)
	b.Tighten(map[string]int{"crates/a.rs": 900, "crates/b.rs": 620})
	if got := b.Render(); got != text {
		t.Errorf("render = %q, want %q", got, text)
	}
}

func TestBaselineDuplicateKeyIsAParseErrorForCountedOnly(t *testing.T) {
	_, err := ParseBaseline("crates/a.rs | 900\ncrates/b.rs | 10\ncrates/a.rs | 800\n", Counted)
	if err == nil {
		t.Fatal("a duplicate counted key must not be silently summed")
	}
	for _, want := range []string{"crates/a.rs", "1", "3"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	b, err := ParseBaseline("crates/a.rs | v.clamp(0.0, 1.0)\ncrates/a.rs | v.clamp(0.0, 1.0)\n", Multiset)
	if err != nil {
		t.Fatalf("repetition IS the count in a multiset: %v", err)
	}
	if b.Counts()["crates/a.rs | v.clamp(0.0, 1.0)"] != 2 {
		t.Errorf("counts = %v", b.Counts())
	}
}

func TestBaselineMalformedCountedLineIsAParseError(t *testing.T) {
	if _, err := ParseBaseline("crates/a.rs | many\n", Counted); err == nil {
		t.Error("a non-integer count must fail loudly")
	}
	if _, err := ParseBaseline("crates/a.rs\n", Counted); err == nil {
		t.Error("a missing count column must fail loudly")
	}
}

func TestBaselineWriteIfChangedIsByteStableAndAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.txt")
	if err := os.WriteFile(path, []byte("crates/a.rs | 600\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBaseline("crates/a.rs | 600\n", Counted)
	wrote, err := b.WriteIfChanged(path)
	if err != nil || wrote {
		t.Fatalf("unchanged content must not be rewritten (wrote=%v err=%v)", wrote, err)
	}

	b.Tighten(map[string]int{"crates/a.rs": 500})
	if wrote, err = b.WriteIfChanged(path); err != nil || !wrote {
		t.Fatalf("a tightened baseline must be written (wrote=%v err=%v)", wrote, err)
	}
	if got := read(t, path); got != "crates/a.rs | 500\n" {
		t.Errorf("file = %q", got)
	}
	if wrote, _ = b.WriteIfChanged(path); wrote {
		t.Error("a second write of the same content must be a no-op")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the atomic rename left files behind: %v", entries)
	}
}

func TestBaselineWriteIfChangedPreservesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.txt")
	if err := os.WriteFile(path, []byte("# header\r\ncrates/a.rs | 900\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := ParseBaseline("# header\ncrates/a.rs | 900\n", Counted)
	if wrote, _ := b.WriteIfChanged(path); wrote {
		t.Error("an unchanged CRLF checkout must not be rewritten")
	}
	b.Tighten(map[string]int{"crates/a.rs": 650})
	if _, err := b.WriteIfChanged(path); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "# header\r\ncrates/a.rs | 650\r\n" {
		t.Errorf("file = %q — CRLF must survive a rewrite", got)
	}
}

func TestLoadBaselineTreatsAnAbsentFileAsEmpty(t *testing.T) {
	b, err := LoadBaseline(filepath.Join(t.TempDir(), "nope.txt"), Multiset)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	if len(b.Counts()) != 0 {
		t.Errorf("counts = %v", b.Counts())
	}
	if r := b.Regressions(map[string]int{"a | b": 1}); len(r) != 1 {
		t.Errorf("every hit against an absent baseline is a regression: %+v", r)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
