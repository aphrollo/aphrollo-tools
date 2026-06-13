package refactor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// Show returns the source text of the symbol named symbol in file, located via
// the language server's document symbols. It lets an agent read one definition
// without reading the whole file. Errors if the symbol is not found.
func Show(ctx context.Context, file, symbol string) (string, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	srcBytes, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	src := string(srcBytes)
	// One read: the symbol ranges below are computed by the server against this
	// exact src (via documentSymbols), and linesInRange slices the same src — so
	// a concurrent edit can never misalign ranges against stale bytes.
	syms, err := documentSymbols(ctx, abs, src)
	if err != nil {
		return "", err
	}
	sym, ok := findSymbol(syms, symbol)
	if !ok {
		return "", fmt.Errorf("symbol %q not found in %s", symbol, file)
	}
	return linesInRange(src, sym.Range), nil
}

// linesInRange returns the full source lines spanned by r (0-based, inclusive
// of the end line), each terminated by a newline. Line indices past the end of
// src are clamped so an over-long range never panics.
func linesInRange(src string, r lsp.Range) string {
	lines := strings.Split(src, "\n")
	// A trailing newline yields a final empty element that is not a real line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := max(r.Start.Line, 0)
	end := min(r.End.Line, len(lines)-1)
	if start > end {
		return ""
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String()
}

// findSymbol returns the first symbol matching name, searching the tree
// depth-first (a parent before its children). An exact name match always wins;
// failing that, name is matched against each symbol's bare identifier — the
// part after the last "." — so a method like "(*Session).DocumentSymbol" is
// reachable by its method name "DocumentSymbol". The boolean is false if
// nothing matches.
func findSymbol(syms []lsp.DocumentSymbol, name string) (lsp.DocumentSymbol, bool) {
	if found, ok := findSymbolBy(syms, func(s lsp.DocumentSymbol) bool { return s.Name == name }); ok {
		return found, true
	}
	return findSymbolBy(syms, func(s lsp.DocumentSymbol) bool { return bareName(s.Name) == name })
}

// findSymbolBy returns the first symbol satisfying match, depth-first.
func findSymbolBy(syms []lsp.DocumentSymbol, match func(lsp.DocumentSymbol) bool) (lsp.DocumentSymbol, bool) {
	for _, s := range syms {
		if match(s) {
			return s, true
		}
		if found, ok := findSymbolBy(s.Children, match); ok {
			return found, true
		}
	}
	return lsp.DocumentSymbol{}, false
}

// bareName strips any receiver/container qualifier from an LSP symbol name,
// returning the identifier after the last ".".
func bareName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}
