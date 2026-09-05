package refactor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
		return emptyEditIsNotReady(sess.Rename(ctx, abs, pos, req.NewName))
	})
	// An empty edit that survived the whole retry budget is a server that
	// really has no rename here, not one still loading: drop the not-ready
	// wrapper so the precise "may not be renameable" diagnostic below fires
	// instead of the loading message.
	if errors.Is(err, errEmptyEditWhileLoading) {
		err = nil
	}
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

	return applyFileEdits(fileEdits, root, abs, src, req.Apply)
}

// samePath reports whether two paths name the same file. The server-supplied
// path comes back from URIToPath with an upper-cased drive while the caller's
// path keeps whatever they typed, so on Windows — where the filesystem is
// case-insensitive anyway — the comparison folds case. Elsewhere it is exact.
// The two strings may also differ in REPRESENTATION rather than case — one
// traversing a symlinked directory the other does not, which is ordinary on
// macOS where the system temp dir itself is a symlink — so a raw (or even
// case-folded) string mismatch is resolved by comparing the files the two
// paths actually name before concluding they differ. This is what makes the
// indexed-source optimization in applyFileEdits engage whenever the two names
// refer to the same file, closing the TOCTOU window a path-string mismatch
// would otherwise reopen.
func samePath(a, b string) bool {
	if samePathOn(runtime.GOOS, a, b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	return samePathOn(runtime.GOOS, ra, rb)
}

// samePathOn is samePath with the platform passed in rather than read from
// runtime.GOOS, so BOTH branches are reachable from a test on either OS. With
// the check written against runtime.GOOS directly, the windows branch could
// only be asserted on windows and the exact branch only off it, so whichever
// platform measured mutants left the other branch's condition unconstrained —
// negating it there changed nothing any test could see.
func samePathOn(goos, a, b string) bool {
	if goos == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return a == b
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
//
// root is the project root the rename is scoped to. Every target path is a
// server-supplied WorkspaceEdit path, so each is checked to live within root
// before any write — a buggy or hostile server cannot redirect a rename onto an
// arbitrary file outside the project (e.g. ~/.bashrc). A path that is ITSELF
// inside root but a SYMLINK to somewhere outside it is checked too: the write
// resolves the link exactly once here — feeding both this check and the write
// below — and is refused if the resolved destination escapes root, naming both
// the link and where it actually points. Resolving once (rather than again at
// write time) closes the TOCTOU window a second resolution would reopen: a
// link that changed between the check and the write would otherwise be
// resolved-and-trusted without ever being re-checked. The whole batch is
// refused on the first out-of-root path (lexical or resolved), before phase 2
// mutates anything.
func applyFileEdits(fileEdits []lsp.FileEdit, root, mainPath, mainSrc string, apply bool) (*RenameResult, error) {
	type pendingWrite struct {
		path, content string // path is the RESOLVED write target (see resolveWriteTarget)
	}
	result := &RenameResult{Applied: apply}
	writes := make([]pendingWrite, 0, len(fileEdits))

	for _, fe := range fileEdits {
		if root != "" && !withinRoot(fe.Path, root) {
			return nil, fmt.Errorf("refusing edit outside project root: %s is not within %s", fe.Path, root)
		}
		target, err := resolveWriteTarget(fe.Path)
		if err != nil {
			return nil, err
		}
		if root != "" && !withinRoot(target, root) {
			return nil, fmt.Errorf("refusing edit: %s is a symlink resolving to %s, which is outside project root %s", fe.Path, target, root)
		}
		before := mainSrc
		if !samePath(fe.Path, mainPath) {
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
		writes = append(writes, pendingWrite{path: target, content: after})
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

// resolveWriteTarget follows path if it is a symlink, returning the real file
// a write should land on: writeFileAtomic's rename-based swap replaces
// whatever sits AT the path it is given, so handing it a symlink path directly
// would replace the LINK with a plain file rather than writing through it —
// the link's former target would be left untouched (or, if nothing else
// references it, effectively orphaned) and the path would silently stop being
// a symlink. An ordinary file, or a path that does not exist yet, is returned
// unchanged. A symlink whose target cannot be resolved (broken link, or a
// cycle) is refused rather than guessed at. Callers that also enforce
// containment (applyFileEdits) must check the RESOLVED result, not just path
// itself — see applyFileEdits' doc comment.
func resolveWriteTarget(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a symlink whose target cannot be resolved: %w", path, err)
	}
	return resolved, nil
}

// writeFileAtomic writes content to path via a same-directory temp file and a
// rename, so a reader never observes a partially written file and an aborted
// write leaves the original intact. The existing file's permission bits are
// preserved (falling back to 0644 for a new file). It performs no symlink
// resolution or containment check of its own — a caller writing through a
// possible symlink (applyFileEdits) must resolve it FIRST via
// resolveWriteTarget and check containment on the result, then pass that
// resolved path here, so this function and the caller's check never disagree
// about which file a write actually lands on.
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

// withinRoot reports whether path is root itself or lies beneath it, comparing
// cleaned absolute paths so "." / ".." segments and a missing leading slash
// can't smuggle an escape past the check.
func withinRoot(path, root string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
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
