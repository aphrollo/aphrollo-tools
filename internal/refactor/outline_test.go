package refactor

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

func rng(sl, sc, el, ec int) lsp.Range {
	return lsp.Range{
		Start: lsp.Position{Line: sl, Character: sc},
		End:   lsp.Position{Line: el, Character: ec},
	}
}

func TestRenderOutline(t *testing.T) {
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

	got := RenderOutline(syms)
	want := "L3-5\tfunc Greet\n" +
		"L7-10\tstruct Server\n" +
		"  L8-8\tfield Addr\n"
	if got != want {
		t.Fatalf("renderOutline mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
