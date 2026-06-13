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
