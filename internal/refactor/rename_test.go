package refactor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// edit is a tiny helper for a single-line [startCol,endCol) replacement.
func edit(line, startCol, endCol int, newText string) lsp.TextEdit {
	return lsp.TextEdit{
		Range: lsp.Range{
			Start: lsp.Position{Line: line, Character: startCol},
			End:   lsp.Position{Line: line, Character: endCol},
		},
		NewText: newText,
	}
}

// A rename apply is two-phase: if any file's edits fail to compute, NO file may
// be written. The old interleaved loop wrote earlier files before hitting the
// bad one, leaving a half-applied workspace.
func TestApplyFileEdits_TransactionalOnComputeError(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.txt")
	bad := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(good, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fileEdits := []lsp.FileEdit{
		{Path: good, Edits: []lsp.TextEdit{edit(0, 0, 5, "HOWDY")}}, // valid
		{Path: bad, Edits: []lsp.TextEdit{edit(0, 40, 50, "boom")}}, // out of range → ApplyEdits errors
	}

	// mainPath empty so both files are read from disk; root=dir contains both.
	if _, err := applyFileEdits(fileEdits, dir, "", "", true); err == nil {
		t.Fatalf("applyFileEdits: want compute error, got nil")
	}

	got, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("good.txt = %q, want unchanged %q — a failed batch must write nothing", got, "hello")
	}
}

// The target file's edits must resolve against the source the server indexed
// (mainSrc), not a fresh disk read, to avoid a TOCTOU mismatch. Here the on-disk
// bytes differ from mainSrc; a disk re-read would put the edit out of range.
func TestApplyFileEdits_TargetUsesIndexedSource(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.txt")
	if err := os.WriteFile(main, []byte("B"), 0o644); err != nil { // stale/short on disk
		t.Fatal(err)
	}
	const indexed = "AAAA" // what the server actually analysed

	fileEdits := []lsp.FileEdit{
		{Path: main, Edits: []lsp.TextEdit{edit(0, 0, 4, "Z")}}, // valid vs "AAAA", out of range vs "B"
	}

	res, err := applyFileEdits(fileEdits, dir, main, indexed, true)
	if err != nil {
		t.Fatalf("applyFileEdits: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("got %d file diffs, want 1", len(res.Files))
	}
	got, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Z" {
		t.Fatalf("main.txt = %q, want %q (edit applied against indexed source)", got, "Z")
	}
}

// A WorkspaceEdit is server-controlled. A buggy or hostile language server can
// return an edit for a path OUTSIDE the project root; applyFileEdits must refuse
// the whole batch before writing anything, so a rename can never clobber an
// arbitrary file like ~/.bashrc.
func TestApplyFileEdits_RejectsEditOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim.txt") // a sibling temp dir, not under root
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	fileEdits := []lsp.FileEdit{
		{Path: outside, Edits: []lsp.TextEdit{edit(0, 0, 8, "PWNED")}},
	}

	if _, err := applyFileEdits(fileEdits, root, "", "", true); err == nil {
		t.Fatalf("applyFileEdits: want containment error for path outside root, got nil")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("victim.txt = %q, want unchanged %q — an out-of-root edit must not be written", got, "original")
	}
}

// samePath folds case on Windows (a case-insensitive filesystem) and compares
// exactly everywhere else. This test runs the real runtime.GOOS check the
// function itself makes, so on a non-Windows runner it has nothing to prove
// and skips rather than asserting the opposite branch it cannot exercise.
func TestSamePath_FoldsCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-folding comparison only applies on windows")
	}
	a := `C:\Foo\Bar.go`
	b := `C:\foo\bar.go`
	if !samePath(a, b) {
		t.Fatalf("samePath(%q, %q) = false, want true — windows folds case", a, b)
	}
}
