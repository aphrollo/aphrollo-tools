package lsp

import "testing"

func TestURIToPath(t *testing.T) {
	cases := []struct {
		uri  DocumentURI
		want string
	}{
		{"file:///home/u/a.go", "/home/u/a.go"},
		{"file:///home/u/a%20b.go", "/home/u/a b.go"}, // percent-decoded
	}
	for _, c := range cases {
		got, err := URIToPath(c.uri)
		if err != nil {
			t.Fatalf("URIToPath(%q): %v", c.uri, err)
		}
		if got != c.want {
			t.Fatalf("URIToPath(%q) = %q, want %q", c.uri, got, c.want)
		}
	}

	if _, err := URIToPath("https://example.com/x"); err == nil {
		t.Fatalf("URIToPath of non-file scheme: want error, got nil")
	}
}

// A WorkspaceEdit's per-file edits must come back grouped by path and sorted
// deterministically, regardless of which LSP shape (changes vs documentChanges)
// the server used.
func TestWorkspaceEdit_FileEdits_Changes(t *testing.T) {
	te := func(c int) TextEdit {
		return TextEdit{Range: Range{Start: Position{0, c}, End: Position{0, c + 1}}, NewText: "x"}
	}
	w := WorkspaceEdit{
		Changes: map[DocumentURI][]TextEdit{
			"file:///proj/zeta.go":  {te(2)},
			"file:///proj/alpha.go": {te(0), te(5)},
		},
	}

	files, err := w.FileEdits()
	if err != nil {
		t.Fatalf("FileEdits: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Path != "/proj/alpha.go" || files[1].Path != "/proj/zeta.go" {
		t.Fatalf("files not sorted by path: %q, %q", files[0].Path, files[1].Path)
	}
	if len(files[0].Edits) != 2 {
		t.Fatalf("alpha.go edits = %d, want 2", len(files[0].Edits))
	}
}
