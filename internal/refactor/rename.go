package refactor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/diff"
	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// RenameRequest describes a rename-symbol operation. Position is given either
// explicitly (Line+Col, 1-based) or by naming the symbol on Line (Symbol),
// which is resolved to the correct UTF-16 column.
type RenameRequest struct {
	File    string
	Line    int    // 1-based
	Col     int    // 1-based UTF-16 column; 0 means resolve via Symbol
	Symbol  string // old name, used when Col == 0
	NewName string
	Apply   bool
}

// FileDiff is the per-file outcome of a rename.
type FileDiff struct {
	Path string
	Diff string
}

// RenameResult is the set of per-file changes a rename would (or did) make.
type RenameResult struct {
	Files   []FileDiff
	Applied bool
}

// Rename performs a language-server-backed rename and returns per-file unified
// diffs. With req.Apply set, the edits are also written to disk.
func Rename(ctx context.Context, req RenameRequest) (*RenameResult, error) {
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
		root = filepath.Dir(abs) // single-file fallback
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
	we, err := sess.Rename(ctx, abs, pos, req.NewName)
	if err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}
	_ = sess.Shutdown(ctx)

	fileEdits, err := we.FileEdits()
	if err != nil {
		return nil, err
	}
	if len(fileEdits) == 0 {
		return nil, fmt.Errorf("no edits produced: symbol may not be renameable at %s:%d:%d", req.File, req.Line, pos.Character+1)
	}

	result := &RenameResult{Applied: req.Apply}
	for _, fe := range fileEdits {
		before, err := os.ReadFile(fe.Path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", fe.Path, err)
		}
		after, err := lsp.ApplyEdits(string(before), fe.Edits)
		if err != nil {
			return nil, fmt.Errorf("apply edits to %s: %w", fe.Path, err)
		}
		result.Files = append(result.Files, FileDiff{
			Path: fe.Path,
			Diff: diff.Unified(relOrAbs(fe.Path), string(before), after),
		})
		if req.Apply {
			if err := os.WriteFile(fe.Path, []byte(after), 0o644); err != nil {
				return nil, fmt.Errorf("write %s: %w", fe.Path, err)
			}
		}
	}
	return result, nil
}

// resolvePosition converts a 1-based line plus an explicit column or a named
// symbol into a 0-based LSP position.
func resolvePosition(src string, lineNo, col int, symbol string) (lsp.Position, error) {
	if lineNo < 1 {
		return lsp.Position{}, fmt.Errorf("line must be 1-based (got %d)", lineNo)
	}
	line := lineNo - 1
	if col > 0 {
		return lsp.Position{Line: line, Character: col - 1}, nil
	}
	if symbol == "" {
		return lsp.Position{}, fmt.Errorf("provide --col or --symbol to locate the target")
	}
	lines := strings.Split(src, "\n")
	if lineNo > len(lines) {
		return lsp.Position{}, fmt.Errorf("line %d is past end of file (%d lines)", lineNo, len(lines))
	}
	c, err := symbolColumn(lines[line], symbol)
	if err != nil {
		return lsp.Position{}, fmt.Errorf("%w on line %d", err, lineNo)
	}
	return lsp.Position{Line: line, Character: c}, nil
}

func relOrAbs(path string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}
