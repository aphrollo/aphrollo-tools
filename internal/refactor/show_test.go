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
