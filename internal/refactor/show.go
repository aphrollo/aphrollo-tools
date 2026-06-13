package refactor

import (
	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// findSymbol returns the first symbol named name, searching the tree
// depth-first (a parent before its children). The boolean is false if no
// symbol matches.
func findSymbol(syms []lsp.DocumentSymbol, name string) (lsp.DocumentSymbol, bool) {
	for _, s := range syms {
		if s.Name == name {
			return s, true
		}
		if found, ok := findSymbol(s.Children, name); ok {
			return found, true
		}
	}
	return lsp.DocumentSymbol{}, false
}
