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
	syms, err := Outline(ctx, file)
	if err != nil {
		return "", err
	}
	sym, ok := findSymbol(syms, symbol)
	if !ok {
		return "", fmt.Errorf("symbol %q not found in %s", symbol, file)
	}
	return linesInRange(string(srcBytes), sym.Range), nil
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
