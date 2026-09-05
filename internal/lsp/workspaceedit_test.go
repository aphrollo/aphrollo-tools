package lsp

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDriveRootedPath_NamesTheDriveAndDropsTheURIsRootSlash: a server answers
// `file:///C:/Users/u/a.go`, and the URL path `/C:/Users/u/a.go` is not a path
// Windows can open — the drive letter must come first, native separators. A
// bare drive root (`/C:`, three characters) is the shortest such path, and a
// server answering with the drive lower-cased must yield the same path the
// caller opened.
func TestDriveRootedPath_NamesTheDriveAndDropsTheURIsRootSlash(t *testing.T) {
	for uriPath, want := range map[string]string{
		"/C:/Users/u/a.go": filepath.FromSlash("C:/Users/u/a.go"),
		"/c:/Users/u/a.go": filepath.FromSlash("C:/Users/u/a.go"),
		"/C:":              "C:",
	} {
		got, ok := DriveRootedPath(uriPath)
		if !ok || got != want {
			t.Errorf("DriveRootedPath(%q) = %q,%v; want %q,true", uriPath, got, ok, want)
		}
	}
	// Everything else is an ordinary path this must not touch.
	for _, uriPath := range []string{"/home/u/a.go", "/C", "C:/x", "//C:/x", "/1:/x"} {
		if got, ok := DriveRootedPath(uriPath); ok {
			t.Errorf("DriveRootedPath(%q) = %q,true; want false", uriPath, got)
		}
	}
}

// TestURIToPath_TakesTheDriveBranchOnWindowsOnly: on Linux `/c:/x` is a legal
// absolute path, so rewriting it there would drop its root slash and change
// its first segment.
func TestURIToPath_TakesTheDriveBranchOnWindowsOnly(t *testing.T) {
	want := "/C:/Users/u/a.go"
	if runtime.GOOS == "windows" {
		want = filepath.FromSlash("C:/Users/u/a.go")
	}
	got, err := URIToPath("file:///C:/Users/u/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("URIToPath(file:///C:/Users/u/a.go) on %s = %q, want %q", runtime.GOOS, got, want)
	}
}

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

	// A file URI with a non-empty authority (host) is not a plain local path; the
	// host must not be silently dropped, turning file://evil/etc/passwd into
	// /etc/passwd. And a percent-encoded traversal must be rejected, not decoded
	// into a path that climbs out of the project.
	for _, bad := range []DocumentURI{
		"file://evil.example.com/etc/passwd",    // non-empty host
		"file:///proj/%2e%2e/%2e%2e/etc/passwd", // encoded ".." traversal
	} {
		if _, err := URIToPath(bad); err == nil {
			t.Fatalf("URIToPath(%q): want error, got nil", bad)
		}
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

// The documentChanges shape must merge the same way Changes does: grouped by
// path and sorted. Constructed by unmarshaling actual JSON (rather than a Go
// literal) since DocumentChanges is now decoded as raw messages, matching how
// a real LSP response arrives over the wire.
func TestWorkspaceEdit_FileEdits_DocumentChanges(t *testing.T) {
	raw := `{"documentChanges": [
		{"textDocument":{"uri":"file:///proj/zeta.go"},
		 "edits":[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":3}},"newText":"x"}]},
		{"textDocument":{"uri":"file:///proj/alpha.go"},
		 "edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"},
		          {"range":{"start":{"line":0,"character":5},"end":{"line":0,"character":6}},"newText":"x"}]}
	]}`
	var w WorkspaceEdit
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
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

// A documentChanges array may mix a resource operation (CreateFile/RenameFile/
// DeleteFile) in among ordinary TextDocumentEdits — gopls emits exactly this
// shape for a package rename. FileEdits must refuse the whole batch with an
// error naming the operation's kind and the document it targets, not decode
// the resource op into a zero TextDocumentEdit and fail on an empty path that
// names nothing (the old `not a file URI: ""` error).
func TestWorkspaceEdit_FileEdits_DocumentChangesNamesAResourceOperationItCannotApply(t *testing.T) {
	raw := `{"documentChanges": [
		{"kind":"rename","oldUri":"file:///proj/old.go","newUri":"file:///proj/new.go"},
		{"textDocument":{"uri":"file:///proj/a.go"},
		 "edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}]}
	]}`
	var w WorkspaceEdit
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	_, err := w.FileEdits()
	if err == nil {
		t.Fatal("FileEdits with a rename resource operation: want error, got nil")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Errorf("error %q does not name the operation kind (rename)", err.Error())
	}
	if !strings.Contains(err.Error(), "file:///proj/new.go") {
		t.Errorf("error %q does not name the target document", err.Error())
	}
}
