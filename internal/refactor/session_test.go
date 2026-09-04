package refactor

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

func TestSession_Rename(t *testing.T) {
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	conn := lsp.NewConn(cw, cr)
	defer conn.Close()

	var gotURI, gotName string
	var gotLine, gotChar int
	srv := fakeLSP{
		renameResult: `{"changes":{"file:///proj/a.go":[` +
			`{"range":{"start":{"line":5,"character":2},"end":{"line":5,"character":5}},"newText":"Bar"}]}}`,
		check: func(uri string, line, char int, newName string) {
			gotURI, gotLine, gotChar, gotName = uri, line, char, newName
		},
	}
	go srv.serve(t, bufio.NewReader(sr), sw)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess := NewSession(conn, "/proj")
	if err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	we, err := sess.Rename(ctx, "/proj/a.go", lsp.Position{Line: 5, Character: 2}, "Bar")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	// Request was well-formed: file URI + 0-based position + new name.
	if gotURI != "file:///proj/a.go" || gotLine != 5 || gotChar != 2 || gotName != "Bar" {
		t.Fatalf("rename request = (%q,%d,%d,%q), want (file:///proj/a.go,5,2,Bar)", gotURI, gotLine, gotChar, gotName)
	}

	// Response parsed into a WorkspaceEdit.
	files, err := we.FileEdits()
	if err != nil {
		t.Fatalf("FileEdits: %v", err)
	}
	if len(files) != 1 || files[0].Path != "/proj/a.go" {
		t.Fatalf("FileEdits = %+v, want one edit for /proj/a.go", files)
	}
	if files[0].Edits[0].NewText != "Bar" {
		t.Fatalf("edit NewText = %q, want Bar", files[0].Edits[0].NewText)
	}
}

// TestPathToURI_EncodesAWindowsDrivePathWithForwardSlashes: url.URL alone turns
// `C:\Users\u\a.go` into `file://C:%5CUsers%5Cu%5Ca.go`, which gopls answers
// with "no package metadata" and rust-analyzer exits on. The LSP form is
// `file:///C:/Users/u/a.go`.
func TestPathToURI_EncodesAWindowsDrivePathWithForwardSlashes(t *testing.T) {
	if got := string(pathToURI(`C:\Users\u\a.go`)); got != "file:///C:/Users/u/a.go" {
		t.Errorf("pathToURI(C:\\Users\\u\\a.go) = %q, want file:///C:/Users/u/a.go", got)
	}
	if got := string(pathToURI("/home/u/a.go")); got != "file:///home/u/a.go" {
		t.Errorf("pathToURI(/home/u/a.go) = %q, want file:///home/u/a.go", got)
	}
}

// A server that negotiates a non-UTF-16 position encoding must be rejected:
// our edit application assumes UTF-16, so proceeding would silently corrupt
// positions.
func TestSession_Initialize_RejectsNonUTF16Encoding(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	conn := lsp.NewConn(cw, cr)
	defer conn.Close()

	srv := fakeLSP{initializeResult: `{"capabilities":{"positionEncoding":"utf-8","renameProvider":true}}`}
	go srv.serve(t, bufio.NewReader(sr), sw)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess := NewSession(conn, "/proj")
	if err := sess.Initialize(ctx); err == nil {
		t.Fatalf("Initialize: want error for utf-8 server, got nil")
	}
}

func TestSession_DocumentSymbol(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	conn := lsp.NewConn(cw, cr)
	defer conn.Close()

	srv := fakeLSP{
		documentSymbolResult: `[` +
			`{"name":"Greet","kind":12,"range":{"start":{"line":2,"character":0},"end":{"line":4,"character":1}},"selectionRange":{"start":{"line":2,"character":5},"end":{"line":2,"character":10}}},` +
			`{"name":"Server","kind":23,"range":{"start":{"line":6,"character":0},"end":{"line":9,"character":1}},"selectionRange":{"start":{"line":6,"character":5},"end":{"line":6,"character":11}},"children":[` +
			`{"name":"Addr","kind":8,"range":{"start":{"line":7,"character":1},"end":{"line":7,"character":12}},"selectionRange":{"start":{"line":7,"character":1},"end":{"line":7,"character":5}}}]}]`,
	}
	go srv.serve(t, bufio.NewReader(sr), sw)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess := NewSession(conn, "/proj")
	if err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	syms, err := sess.DocumentSymbol(ctx, "/proj/a.go")
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("got %d top-level symbols, want 2", len(syms))
	}
	if syms[0].Name != "Greet" || syms[0].Kind != lsp.KindFunction {
		t.Fatalf("sym[0] = %+v, want Greet/func", syms[0])
	}
	if syms[1].Name != "Server" || syms[1].Kind != lsp.KindStruct {
		t.Fatalf("sym[1] = %+v, want Server/struct", syms[1])
	}
	if len(syms[1].Children) != 1 || syms[1].Children[0].Name != "Addr" {
		t.Fatalf("Server children = %+v, want [Addr]", syms[1].Children)
	}
	if syms[1].Range.Start.Line != 6 {
		t.Fatalf("Server range start line = %d, want 6", syms[1].Range.Start.Line)
	}
}

// A server that ignores hierarchicalDocumentSymbolSupport and returns the flat
// SymbolInformation[] form decodes into DocumentSymbol with zero-value ranges
// (the range lives under "location" instead). That would silently produce
// L1-1 outlines and wrong show output, so DocumentSymbol must reject it loudly.
func TestSession_DocumentSymbol_RejectsFlatSymbolInformation(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	conn := lsp.NewConn(cw, cr)
	defer conn.Close()

	srv := fakeLSP{
		// SymbolInformation: name + kind present, but the range is nested under
		// "location", so the top-level Range decodes to zero.
		documentSymbolResult: `[{"name":"Greet","kind":12,"location":{"uri":"file:///proj/a.go","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":5}}}}]`,
	}
	go srv.serve(t, bufio.NewReader(sr), sw)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess := NewSession(conn, "/proj")
	if err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := sess.DocumentSymbol(ctx, "/proj/a.go"); err == nil {
		t.Fatalf("DocumentSymbol: want error for flat SymbolInformation, got nil")
	}
}

func TestSession_References(t *testing.T) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	conn := lsp.NewConn(cw, cr)
	defer conn.Close()

	srv := fakeLSP{
		referencesResult: `[` +
			`{"uri":"file:///proj/a.go","range":{"start":{"line":2,"character":5},"end":{"line":2,"character":8}}},` +
			`{"uri":"file:///proj/b.go","range":{"start":{"line":9,"character":1},"end":{"line":9,"character":4}}}]`,
	}
	go srv.serve(t, bufio.NewReader(sr), sw)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess := NewSession(conn, "/proj")
	if err := sess.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	locs, err := sess.References(ctx, "/proj/a.go", lsp.Position{Line: 2, Character: 5}, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("got %d locations, want 2", len(locs))
	}
	if locs[0].URI != "file:///proj/a.go" || locs[0].Range.Start.Line != 2 {
		t.Fatalf("loc[0] = %+v, unexpected", locs[0])
	}
}
