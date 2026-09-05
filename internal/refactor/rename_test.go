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

// The indexed-source optimization must engage even when the server's path
// string for the target file differs in REPRESENTATION from the caller's own
// (e.g. one traverses a symlinked directory the other does not) as long as
// both name the same file. A plain string comparison misses this and falls
// into the disk-read branch, reintroducing the TOCTOU window the two-phase
// design exists to close.
func TestApplyFileEdits_TargetUsesIndexedSourceThroughSymlinkedDirectory(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(real, "main.txt")
	if err := os.WriteFile(mainPath, []byte("B"), 0o644); err != nil { // stale/short on disk
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// Same file as mainPath, reached through the symlink — a different string.
	serverPath := filepath.Join(link, "main.txt")
	const indexed = "AAAA" // what the server actually analysed

	fileEdits := []lsp.FileEdit{
		{Path: serverPath, Edits: []lsp.TextEdit{edit(0, 0, 4, "Z")}}, // valid vs "AAAA", out of range vs "B"
	}

	res, err := applyFileEdits(fileEdits, dir, mainPath, indexed, true)
	if err != nil {
		t.Fatalf("applyFileEdits: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("got %d file diffs, want 1", len(res.Files))
	}
	got, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Z" {
		t.Fatalf("main.txt = %q, want %q (edit applied against indexed source reached through a symlinked path)", got, "Z")
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

// writeFileAtomic's rename step must write THROUGH a symlinked target rather
// than replacing the link itself: os.Rename(tmp, path) on a path that is a
// symlink swaps in a plain file at that path, dropping the link and leaving
// its former target's content stale (or orphaned). This asserts on the real
// filesystem state after the write — path must still be a symlink, and its
// resolved target must hold the new content — because a check on the returned
// error or a printed message alone would not have caught the conversion.
func TestWriteFileAtomic_WritesThroughASymlinkRatherThanReplacingIt(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(link, "new"); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat(%s): %v", link, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink after writeFileAtomic — the link was replaced by a plain file", link)
	}
	dest, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink(%s): %v", link, err)
	}
	if dest != real {
		t.Fatalf("symlink now points at %q, want unchanged target %q", dest, real)
	}
	gotViaLink, err := os.ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotViaLink) != "new" {
		t.Fatalf("content read through the link = %q, want %q", gotViaLink, "new")
	}
	gotViaReal, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotViaReal) != "new" {
		t.Fatalf("real.txt content = %q, want %q — the write must land on the link's target", gotViaReal, "new")
	}
}

// A symlink whose target does not exist cannot be written through; refusing
// loudly is the contract, not silently converting the link into a plain file
// (which writeFileAtomic must not do — see the test above).
func TestWriteFileAtomic_RefusesABrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "broken.txt")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist.txt"), link); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(link, "new"); err == nil {
		t.Fatalf("writeFileAtomic on a broken symlink: want error, got nil")
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat(%s): %v", link, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink after the refused write", link)
	}
}

// samePath folds case on Windows (a case-insensitive filesystem) and compares
// exactly everywhere else. This test runs the real runtime.GOOS check the
// function itself makes, so on a non-Windows runner it has nothing to prove
// and skips rather than asserting the opposite branch it cannot exercise.
// The platform branch is what the mutation gate kept finding unconstrained:
// whichever OS measures mutants can only reach one side of a runtime.GOOS
// check, so the other side's condition is never pinned. samePathOn takes the
// platform as an argument, so both cases are asserted on either OS.

// TestSamePathOn_FoldsCaseForWindows pins the windows branch: the LSP server
// answers with an upper-cased drive letter while the caller keeps what they
// typed, and on a case-insensitive filesystem those name one file.
func TestSamePathOn_FoldsCaseForWindows(t *testing.T) {
	a, b := `C:\Foo\Bar.go`, `C:\foo\bar.go`
	if !samePathOn("windows", a, b) {
		t.Errorf("samePathOn(windows, %q, %q) = false, want true — windows folds case", a, b)
	}
}

// TestSamePathOn_IsExactForEveryOtherPlatform pins the other branch, and is
// the one that kills the negation when mutants are measured on Linux: two
// paths differing only in case are two different files there.
func TestSamePathOn_IsExactForEveryOtherPlatform(t *testing.T) {
	a, b := "/src/Foo.go", "/src/foo.go"
	if samePathOn("linux", a, b) {
		t.Errorf("samePathOn(linux, %q, %q) = true, want false — only windows folds case", a, b)
	}
}

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
