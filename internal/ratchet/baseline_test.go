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

// TestBaseline_AdoptRaisesAndCreatesRows proves Adopt does what Tighten
// deliberately never does: raise an existing key past its old ceiling, and
// create a row for a key the baseline has never seen at all.
func TestBaseline_AdoptRaisesAndCreatesRows(t *testing.T) {
	b, err := ParseBaseline("crates/a.rs | 900\ncrates/b.rs | 620\n", Counted)
	if err != nil {
		t.Fatal(err)
	}
	tt := b.AdoptWithSites(map[string]int{
		"crates/a.rs":   1200, // raised — a real ratchet.Tighten would refuse this
		"crates/new.rs": 5,    // brand new — a real ratchet.Tighten would refuse this too
	}, nil)
	if got, want := b.Render(), "crates/a.rs | 1200\ncrates/new.rs | 5\n"; got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(tt.Removed, []Change{{"crates/b.rs", 620, 0}}) {
		t.Errorf("a key absent from the adopted measure is still dropped: %+v", tt.Removed)
	}
	wantLowered := map[Change]bool{{"crates/a.rs", 900, 1200}: true, {"crates/new.rs", 0, 5}: true}
	if len(tt.Lowered) != len(wantLowered) {
		t.Fatalf("Lowered = %+v", tt.Lowered)
	}
	for _, c := range tt.Lowered {
		if !wantLowered[c] {
			t.Errorf("unexpected change %+v", c)
		}
	}
}

// TestBaseline_AdoptOnAnEmptyBaselineWritesEveryMeasuredRow is the "law has no
// baseline file yet" shape: adopting from zero rows must write every key —
// the exact case where ordinary Tighten writes nothing at all.
func TestBaseline_AdoptOnAnEmptyBaselineWritesEveryMeasuredRow(t *testing.T) {
	b := &Baseline{form: Counted}
	// An ordinary Tighten over an empty baseline adds nothing, by design.
	b.Tighten(map[string]int{"crates/a.rs": 40, "crates/b.rs": 12})
	if got := b.Render(); got != "" {
		t.Fatalf("Tighten must never create a row: render = %q", got)
	}

	b.AdoptWithSites(map[string]int{"crates/a.rs": 40, "crates/b.rs": 12}, nil)
	if got, want := b.Render(), "crates/a.rs | 40\ncrates/b.rs | 12\n"; got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

// TestBaseline_AdoptOnAMultisetFormWritesOneRowPerOccurrence proves adoption
// respects the line-keyed forms' one-row-per-occurrence shape, using the
// sites a real scan would hand it.
func TestBaseline_AdoptOnAMultisetFormWritesOneRowPerOccurrence(t *testing.T) {
	b := &Baseline{form: MultisetByText}
	b.AdoptWithSites(
		map[string]int{"x.clamp(0.0, 1.0)": 2},
		map[string][]string{"x.clamp(0.0, 1.0)": {"crates/a.rs | x.clamp(0.0, 1.0)", "crates/b.rs | x.clamp(0.0, 1.0)"}},
	)
	want := "crates/a.rs | x.clamp(0.0, 1.0)\ncrates/b.rs | x.clamp(0.0, 1.0)\n"
	if got := b.Render(); got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

// TestBaseline_AdoptOnAnExistingMultisetLowersDropsAndCreates proves the first
// pass of AdoptWithSites — the one walking EXISTING rows — for a form keyed
// by occurrence count, not a single row per key: a repeated identity must
// drop exactly the rows past its new target (the seen>=want boundary), an
// identity absent from the new measure must drop every row, and a brand-new
// identity still gets its rows from `sites` in the second pass.
func TestBaseline_AdoptOnAnExistingMultisetLowersDropsAndCreates(t *testing.T) {
	b, err := ParseBaseline(
		"crates/a.rs | x.clamp(0.0, 1.0)\ncrates/b.rs | x.clamp(0.0, 1.0)\ncrates/c.rs | z.sin()\n",
		MultisetByText,
	)
	if err != nil {
		t.Fatal(err)
	}
	tt := b.AdoptWithSites(
		map[string]int{
			"x.clamp(0.0, 1.0)": 1, // lowered from 2 existing rows to 1
			"w.cos()":           2, // brand new identity, no existing row at all
			// "z.sin()" absent: its one existing row must be dropped entirely
		},
		map[string][]string{
			"x.clamp(0.0, 1.0)": {"crates/a.rs | x.clamp(0.0, 1.0)"},
			"w.cos()":           {"crates/d.rs | w.cos()", "crates/e.rs | w.cos()"},
		},
	)

	want := "crates/a.rs | x.clamp(0.0, 1.0)\ncrates/d.rs | w.cos()\ncrates/e.rs | w.cos()\n"
	if got := b.Render(); got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(tt.Removed, []Change{{"z.sin()", 1, 0}}) {
		t.Errorf("removed = %+v", tt.Removed)
	}
	wantLowered := map[Change]bool{
		{"x.clamp(0.0, 1.0)", 2, 1}: true,
		{"w.cos()", 0, 2}:           true,
	}
	if len(tt.Lowered) != len(wantLowered) {
		t.Fatalf("lowered = %+v", tt.Lowered)
	}
	for _, c := range tt.Lowered {
		if !wantLowered[c] {
			t.Errorf("unexpected change %+v", c)
		}
	}
}

// TestBaseline_AdoptIgnoresAZeroOrNegativeMeasuredCount proves a key whose
// measured count is not positive never lands a row — the measure function
// this feeds from counts occurrences, never anything else, but the guard
// must still hold if it ever hands over a zero.
func TestBaseline_AdoptIgnoresAZeroOrNegativeMeasuredCount(t *testing.T) {
	b := &Baseline{form: Counted}
	b.AdoptWithSites(map[string]int{"crates/a.rs": 0, "crates/b.rs": -1, "crates/c.rs": 4}, nil)
	want := "crates/c.rs | 4\n"
	if got := b.Render(); got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

// TestBaseline_AdoptExhaustsKnownSitesThenFallsBackToTheIdentity proves the
// site-assignment loops (both the first pass over existing rows and the
// second pass appending shortfall rows) stop consulting `sites` once its
// list runs out, at the EXACT boundary where the count of rows already
// placed equals the number of known sites, and fall back to the bare
// identity for whatever is left.
func TestBaseline_AdoptExhaustsKnownSitesThenFallsBackToTheIdentity(t *testing.T) {
	// Existing pass: two rows for "q.tan()" kept, but only ONE known site —
	// the first is renamed to it, the second keeps its original path.
	b, err := ParseBaseline(
		"crates/a.rs | q.tan()\ncrates/b.rs | q.tan()\n",
		MultisetByText,
	)
	if err != nil {
		t.Fatal(err)
	}
	b.AdoptWithSites(
		map[string]int{"q.tan()": 2},
		map[string][]string{"q.tan()": {"crates/c.rs | q.tan()"}},
	)
	want := "crates/c.rs | q.tan()\ncrates/b.rs | q.tan()\n"
	if got := b.Render(); got != want {
		t.Errorf("render = %q, want %q", got, want)
	}

	// Shortfall pass: a brand-new identity wants 3 rows, only 2 known sites —
	// the third falls back to the bare identity as its key.
	b2 := &Baseline{form: MultisetByText}
	b2.AdoptWithSites(
		map[string]int{"w.cos()": 3},
		map[string][]string{"w.cos()": {"crates/d.rs | w.cos()", "crates/e.rs | w.cos()"}},
	)
	want2 := "crates/d.rs | w.cos()\ncrates/e.rs | w.cos()\nw.cos()\n"
	if got := b2.Render(); got != want2 {
		t.Errorf("render = %q, want %q", got, want2)
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

// A key that itself ends in "|" produces a rendered line with two " | "
// triples ("<key with |> | <count>" becomes e.g. "0 | | 0"), and that
// rendered text must parse back to the SAME key and count rather than error:
// a first-occurrence split on " | " cuts the key short and leaves a
// non-numeric count field. Found by FuzzBaseline (issue #414); the input is
// its minimized reproducer, a lone mid-line CR ("0 |\r | 0") that trims down
// to a key of "0 |".
func TestBaselineCounted_RoundTripsAKeyEndingInPipe(t *testing.T) {
	b, err := ParseBaseline("0 |\r | 0", Counted)
	if err != nil {
		t.Fatalf("ParseBaseline: %v", err)
	}
	rendered := b.Render()
	if rendered != "0 | | 0\n" {
		t.Fatalf("Render() = %q, want %q", rendered, "0 | | 0\n")
	}
	again, err := ParseBaseline(rendered, Counted)
	if err != nil {
		t.Fatalf("re-parsing Render()'s own output failed: %v\nrendered: %s", err, rendered)
	}
	if got := again.Render(); got != rendered {
		t.Fatalf("re-parse did not reach a fixed point: got %q, want %q", got, rendered)
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
