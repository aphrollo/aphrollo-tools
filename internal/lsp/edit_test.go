package lsp

import "testing"

func TestApplyEdits_SingleLineReplace(t *testing.T) {
	src := "hello world"
	edits := []TextEdit{
		{Range: Range{Start: Position{Line: 0, Character: 6}, End: Position{Line: 0, Character: 11}}, NewText: "gopls"},
	}

	got, err := ApplyEdits(src, edits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "hello gopls"; got != want {
		t.Fatalf("ApplyEdits = %q, want %q", got, want)
	}
}

// Multiple edits in one document must compose regardless of input order;
// renames return many TextEdits per file with no guaranteed ordering.
func TestApplyEdits_MultipleEditsAnyOrder(t *testing.T) {
	src := "foo bar baz"
	edits := []TextEdit{
		{Range: Range{Start: Position{Line: 0, Character: 8}, End: Position{Line: 0, Character: 11}}, NewText: "Y"},
		{Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 3}}, NewText: "X"},
	}

	got, err := ApplyEdits(src, edits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "X bar Y"; got != want {
		t.Fatalf("ApplyEdits = %q, want %q", got, want)
	}
}

// LSP Character offsets count UTF-16 code units, not bytes: a 2-byte rune
// before the edit must not shift the resolved span.
func TestApplyEdits_UTF16Offsets(t *testing.T) {
	src := "café world" // é is one UTF-16 unit but two bytes
	edits := []TextEdit{
		{Range: Range{Start: Position{Line: 0, Character: 5}, End: Position{Line: 0, Character: 10}}, NewText: "X"},
	}

	got, err := ApplyEdits(src, edits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "café X"; got != want {
		t.Fatalf("ApplyEdits = %q, want %q", got, want)
	}
}

// A single edit whose Start is after its End is malformed; splicing it would
// silently DUPLICATE the [End,Start) bytes (a LOSSLESS-contract violation), so
// ApplyEdits must reject it rather than emit corrupt text.
func TestApplyEdits_InvertedRangeIsError(t *testing.T) {
	src := "hello world"
	edits := []TextEdit{
		{Range: Range{Start: Position{Line: 0, Character: 8}, End: Position{Line: 0, Character: 3}}, NewText: "X"},
	}

	if _, err := ApplyEdits(src, edits); err == nil {
		t.Fatalf("ApplyEdits with inverted range: want error, got nil")
	}
}

// Overlapping edits cannot be applied coherently; rather than splice them into
// garbage, ApplyEdits must fail loud (LOSSLESS/VISIBLE principle).
func TestApplyEdits_OverlappingIsError(t *testing.T) {
	src := "hello world"
	edits := []TextEdit{
		{Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 5}}, NewText: "A"},
		{Range: Range{Start: Position{Line: 0, Character: 3}, End: Position{Line: 0, Character: 8}}, NewText: "B"},
	}

	if _, err := ApplyEdits(src, edits); err == nil {
		t.Fatalf("ApplyEdits with overlapping edits: want error, got nil")
	}
}
