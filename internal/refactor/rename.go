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
	we, err := retryWhileLoading(ctx, func() (lsp.WorkspaceEdit, error) {
		return sess.Rename(ctx, abs, pos, req.NewName)
	})
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

	return applyFileEdits(fileEdits, abs, src, req.Apply)
}

// applyFileEdits turns a rename's per-file edits into diffs and, when apply is
// set, writes them. It is two-phase by design (LOSSLESS / idempotent contract):
//
//   - Phase 1 computes every file's new content in memory. The target file
//     (mainPath) is edited against mainSrc — the exact bytes the server
//     indexed — instead of being re-read from disk, closing the TOCTOU window
//     where a concurrent edit between the rename request and the write would
//     resolve server offsets against stale content.
//   - Phase 2 writes only after all edits computed cleanly, each file flipped
//     atomically (temp file + rename) so no file is ever left half-written. A
//     single failing edit aborts the whole batch before any disk mutation.
//
// Cross-file atomicity is not achievable without a transaction, but this
// guarantees all-or-nothing at the point of computation and per-file atomicity
// on write.
func applyFileEdits(fileEdits []lsp.FileEdit, mainPath, mainSrc string, apply bool) (*RenameResult, error) {
	type pendingWrite struct {
		path, content string
	}
	result := &RenameResult{Applied: apply}
	writes := make([]pendingWrite, 0, len(fileEdits))

	for _, fe := range fileEdits {
		before := mainSrc
		if fe.Path != mainPath {
			b, err := os.ReadFile(fe.Path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", fe.Path, err)
			}
			before = string(b)
		}
		after, err := lsp.ApplyEdits(before, fe.Edits)
		if err != nil {
			return nil, fmt.Errorf("apply edits to %s: %w", fe.Path, err)
		}
		result.Files = append(result.Files, FileDiff{
			Path: fe.Path,
			Diff: diff.Unified(relOrAbs(fe.Path), before, after),
		})
		writes = append(writes, pendingWrite{path: fe.Path, content: after})
	}

	if apply {
		for _, w := range writes {
			if err := writeFileAtomic(w.path, w.content); err != nil {
				return nil, fmt.Errorf("write %s: %w", w.path, err)
			}
		}
	}
	return result, nil
}

// writeFileAtomic writes content to path via a same-directory temp file and a
// rename, so a reader never observes a partially written file and an aborted
// write leaves the original intact. The existing file's permission bits are
// preserved (falling back to 0644 for a new file).
func writeFileAtomic(path, content string) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aphrollo-rename-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
