package refactor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// Outline returns the hierarchical symbol tree for file, driving the matching
// language server. It is the read-only counterpart to find-references: an agent
// can map a file's shape without reading its full contents.
func Outline(ctx context.Context, file string) ([]lsp.DocumentSymbol, error) {
	lang, err := DetectLanguage(file)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	srcBytes, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	src := string(srcBytes)

	root, err := FindProjectRoot(filepath.Dir(abs), lang.RootMarkers)
	if err != nil {
		root = filepath.Dir(abs)
	}

	conn, cleanup, err := Spawn(ctx, lang)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	sess := NewSession(conn, root)
	if err := sess.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize %s: %w", lang.Command, err)
	}
	if err := sess.DidOpen(abs, lang.Name, src); err != nil {
		return nil, err
	}
	syms, err := retryWhileLoading(ctx, func() ([]lsp.DocumentSymbol, error) {
		return sess.DocumentSymbol(ctx, abs)
	})
	if err != nil {
		return nil, fmt.Errorf("documentSymbol: %w", err)
	}
	_ = sess.Shutdown(ctx)
	return syms, nil
}

// RenderOutline formats a symbol tree as an indented, line-numbered listing.
// Each line is "<indent>L<start>-<end>\t<kind> <name>", with two spaces of
// indent per nesting level and 1-based inclusive line numbers.
func RenderOutline(syms []lsp.DocumentSymbol) string {
	var b strings.Builder
	var walk func(s lsp.DocumentSymbol, depth int)
	walk = func(s lsp.DocumentSymbol, depth int) {
		indent := strings.Repeat("  ", depth)
		fmt.Fprintf(&b, "%sL%d-%d\t%s %s\n",
			indent, s.Range.Start.Line+1, s.Range.End.Line+1, s.Kind.Name(), s.Name)
		for _, c := range s.Children {
			walk(c, depth+1)
		}
	}
	for _, s := range syms {
		walk(s, 0)
	}
	return b.String()
}
