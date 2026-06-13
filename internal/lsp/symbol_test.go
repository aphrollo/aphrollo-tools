package lsp

import "testing"

func TestSymbolKind_Name(t *testing.T) {
	cases := []struct {
		kind SymbolKind
		want string
	}{
		{KindFunction, "func"},
		{KindMethod, "method"},
		{KindStruct, "struct"},
		{KindInterface, "interface"},
		{KindConstant, "const"},
		{KindVariable, "var"},
		{KindField, "field"},
		{KindClass, "class"},
		{SymbolKind(0), "symbol"},   // out of range below
		{SymbolKind(999), "symbol"}, // out of range above
	}
	for _, c := range cases {
		if got := c.kind.Name(); got != c.want {
			t.Errorf("SymbolKind(%d).Name() = %q, want %q", c.kind, got, c.want)
		}
	}
}
