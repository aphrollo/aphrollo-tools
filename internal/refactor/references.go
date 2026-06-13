package refactor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// RefRequest describes a find-references query. Position is given by Line+Col
// (1-based) or by naming the Symbol on Line.
type RefRequest struct {
	File               string
	Line               int
	Col                int
	Symbol             string
	IncludeDeclaration bool
}

// Reference is one resolved occurrence of a symbol, grep-style (1-based).
type Reference struct {
	Path string
	Line int
	Col  int
	Text string // the source line, trimmed of trailing newline
}

// FindReferences returns every reference to the symbol at the requested
// position, sorted by path then position.
func FindReferences(ctx context.Context, req RefRequest) ([]Reference, error) {
	lang, err := DetectLanguage(req.File)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(req.File)
	if err != nil {
		return nil, err
	}
	srcBytes, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	src := string(srcBytes)

	pos, err := resolvePosition(src, req.Line, req.Col, req.Symbol)
	if err != nil {
		return nil, err
	}

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
	locs, err := sess.References(ctx, abs, pos, req.IncludeDeclaration)
	if err != nil {
		return nil, fmt.Errorf("references: %w", err)
	}
	_ = sess.Shutdown(ctx)

	return locationsToReferences(locs)
}

// locationsToReferences resolves LSP locations to grep-style references,
// annotating each with its source line. File contents are read once per file.
func locationsToReferences(locs []lsp.Location) ([]Reference, error) {
	lineCache := map[string][]string{}
	out := make([]Reference, 0, len(locs))
	for _, loc := range locs {
		path, err := lsp.URIToPath(loc.URI)
		if err != nil {
			return nil, err
		}
		lines, ok := lineCache[path]
		if !ok {
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			lines = strings.Split(string(content), "\n")
			lineCache[path] = lines
		}
		ln := loc.Range.Start.Line
		text := ""
		if ln >= 0 && ln < len(lines) {
			text = lines[ln]
		}
		out = append(out, Reference{
			Path: path,
			Line: ln + 1,
			Col:  loc.Range.Start.Character + 1,
			Text: text,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out, nil
}
