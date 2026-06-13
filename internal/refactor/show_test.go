package refactor

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

func TestFindSymbol(t *testing.T) {
	syms := []lsp.DocumentSymbol{
		{Name: "Greet", Kind: lsp.KindFunction, Range: rng(2, 0, 4, 1)},
		{
			Name:  "Server",
			Kind:  lsp.KindStruct,
			Range: rng(6, 0, 9, 1),
			Children: []lsp.DocumentSymbol{
				{Name: "Addr", Kind: lsp.KindField, Range: rng(7, 1, 7, 12)},
			},
		},
	}

	// Top-level match.
	got, ok := findSymbol(syms, "Greet")
	if !ok || got.Kind != lsp.KindFunction {
		t.Fatalf("findSymbol(Greet) = %+v, %v; want func", got, ok)
	}

	// Nested match found via depth-first walk.
	got, ok = findSymbol(syms, "Addr")
	if !ok || got.Range.Start.Line != 7 {
		t.Fatalf("findSymbol(Addr) = %+v, %v; want nested field at line 7", got, ok)
	}

	// Absent symbol.
	if _, ok := findSymbol(syms, "Missing"); ok {
		t.Fatalf("findSymbol(Missing): want not found")
	}
}

// gopls names a method with its receiver, e.g. "(*Session).DocumentSymbol".
// A user who passes the bare method name should still find it, while an exact
// match always wins over a receiver-qualified one.
func TestFindSymbol_BareMethodName(t *testing.T) {
	syms := []lsp.DocumentSymbol{
		{Name: "Greet", Kind: lsp.KindFunction, Range: rng(2, 0, 4, 1)},
		{Name: "(*Session).DocumentSymbol", Kind: lsp.KindMethod, Range: rng(10, 0, 14, 1)},
		{Name: "Initialize", Kind: lsp.KindFunction, Range: rng(20, 0, 21, 1)},
		{Name: "(*Session).Initialize", Kind: lsp.KindMethod, Range: rng(30, 0, 31, 1)},
	}

	// Bare method name resolves to the receiver-qualified symbol.
	got, ok := findSymbol(syms, "DocumentSymbol")
	if !ok || got.Range.Start.Line != 10 {
		t.Fatalf("findSymbol(DocumentSymbol) = %+v, %v; want method at line 10", got, ok)
	}

	// Exact match wins over the receiver-qualified candidate.
	got, ok = findSymbol(syms, "Initialize")
	if !ok || got.Range.Start.Line != 20 {
		t.Fatalf("findSymbol(Initialize) = %+v, %v; want exact func at line 20", got, ok)
	}
}

func TestLinesInRange(t *testing.T) {
	src := "package m\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n"

	// Greet spans lines 2..4 (0-based, inclusive): the whole declaration.
	got := linesInRange(src, rng(2, 0, 4, 1))
	want := "func Greet() string {\n\treturn \"hi\"\n}\n"
	if got != want {
		t.Fatalf("linesInRange mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}

	// A single-line range returns just that line.
	if got := linesInRange(src, rng(0, 0, 0, 9)); got != "package m\n" {
		t.Fatalf("single-line range = %q, want %q", got, "package m\n")
	}

	// An out-of-range end is clamped to the last line, not a panic.
	if got := linesInRange(src, rng(4, 0, 99, 0)); got != "}\n" {
		t.Fatalf("clamped range = %q, want %q", got, "}\n")
	}
}
