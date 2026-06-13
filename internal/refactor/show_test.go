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
