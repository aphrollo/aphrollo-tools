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

// ratchet: test_removed TestWriteFileAtomic_WritesThroughASymlinkRatherThanReplacingIt: split into
// TestResolveWriteTarget_FollowsASymlinkToItsRealTarget (the resolution, unit-level) and
// TestApplyFileEdits_WritesThroughAnInRootSymlinkRatherThanReplacingIt (the FS-state assertion,
// now at applyFileEdits since resolution moved there so containment could be checked on the
// resolved path too — see rename.go's resolveWriteTarget/applyFileEdits doc comments).
// ratchet: test_removed TestWriteFileAtomic_RefusesABrokenSymlink: split into
// TestResolveWriteTarget_RefusesABrokenSymlink, same reason as above.

// resolveWriteTarget must follow a symlink to its real target: writeFileAtomic
// renames onto whatever path it is handed, so applyFileEdits must resolve a
// symlink itself and hand writeFileAtomic the real path — otherwise the write
// replaces the LINK with a plain file rather than writing through it.
func TestResolveWriteTarget_FollowsASymlinkToItsRealTarget(t *testing.T) {
	dir := resolvedTempDir(t)
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWriteTarget(link)
	if err != nil {
		t.Fatalf("resolveWriteTarget: %v", err)
	}
	if got != real {
		t.Fatalf("resolveWriteTarget(%s) = %q, want the resolved real path %q", link, got, real)
	}
}

// An ordinary (non-symlink) path is returned unchanged.
func TestResolveWriteTarget_LeavesAnOrdinaryPathUnchanged(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWriteTarget(plain)
	if err != nil {
		t.Fatalf("resolveWriteTarget: %v", err)
	}
	if got != plain {
		t.Fatalf("resolveWriteTarget(%s) = %q, want unchanged", plain, got)
	}
}

// A symlink whose target does not exist cannot be resolved to a real path;
// refusing loudly is the contract, not guessing or silently falling back to
// the link path itself (which would let writeFileAtomic convert the link into
// a plain file).
func TestResolveWriteTarget_RefusesABrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "broken.txt")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist.txt"), link); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveWriteTarget(link); err == nil {
		t.Fatalf("resolveWriteTarget on a broken symlink: want error, got nil")
	}
}

// End to end: applyFileEdits must write THROUGH an in-root symlink rather than
// replacing the link itself. This asserts on the real filesystem state after
// the write — the link path must still be a symlink, and its resolved target
// must hold the new content, read both through the link and directly — because
// a check on the returned error or a printed message alone would not have
// caught a silent link-to-plain-file conversion.
func TestApplyFileEdits_WritesThroughAnInRootSymlinkRatherThanReplacingIt(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real.txt")
	if err := os.WriteFile(real, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	fileEdits := []lsp.FileEdit{
		{Path: link, Edits: []lsp.TextEdit{edit(0, 0, 4, "Z")}},
	}
	if _, err := applyFileEdits(fileEdits, root, "", "", true); err != nil {
		t.Fatalf("applyFileEdits: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat(%s): %v", link, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink after applyFileEdits — the link was replaced by a plain file", link)
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
	if string(gotViaLink) != "Z" {
		t.Fatalf("content read through the link = %q, want %q", gotViaLink, "Z")
	}
	gotViaReal, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotViaReal) != "Z" {
		t.Fatalf("real.txt content = %q, want %q — the write must land on the link's target", gotViaReal, "Z")
	}
}

// The containment check must follow a symlink: a path that is LEXICALLY inside
// root but a symlink to somewhere OUTSIDE it must be refused, and the outside
// file must be left byte-identical — an ordinary in-root TextDocumentEdit is
// enough to trigger this, no malformed server response required. This is the
// escape writing through the link (the test above) reopens if containment is
// checked only on the lexical path: withinRoot(fe.Path, root) passes because
// link.txt lexically sits under root, and the write would otherwise land on
// whatever the link resolves to.
func TestApplyFileEdits_RefusesASymlinkResolvingOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir() // a sibling temp dir, not under root
	outside := filepath.Join(outsideDir, "victim.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt") // lexically inside root
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	fileEdits := []lsp.FileEdit{
		{Path: link, Edits: []lsp.TextEdit{edit(0, 0, 8, "PWNED")}},
	}
	if _, err := applyFileEdits(fileEdits, root, "", "", true); err == nil {
		t.Fatalf("applyFileEdits: want containment error for a symlink resolving outside root, got nil")
	}

	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("victim.txt = %q, want unchanged %q — a symlink escape must not be written", got, "original")
	}
	dest, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink(%s): %v", link, err)
	}
	if dest != outside {
		t.Fatalf("link.txt now points at %q, want unchanged target %q", dest, outside)
	}
}

// A project reached through a symlinked ANCESTOR directory (a symlinked
// second-drive checkout, or macOS where /tmp itself is a symlink) must not
// have its own in-project symlinks refused. root is passed to applyFileEdits
// unresolved (as FindProjectRoot returns it); resolveWriteTarget resolves a
// file that is ITSELF a symlink through every symlink on its path, including
// root's own ancestor link, producing a fully-resolved target with no "link"
// segment left in it — so before the fix, comparing that resolved target
// against the still-unresolved root refused a legitimate in-project symlink
// (via_link.txt, resolving to target.txt in the same directory) purely
// because root's ancestor happened to be a symlink too, calling a file that
// never left the project "outside project root".
func TestApplyFileEdits_AppliesEditToInProjectSymlinkThroughSymlinkedRootAncestor(t *testing.T) {
	base := t.TempDir() // t.TempDir() already resolves symlinks in its own path
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	root := link // the project root, reached through the symlinked ancestor

	target := filepath.Join(real, "target.txt")
	if err := os.WriteFile(target, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(real, "via_link.txt") // an ordinary in-project symlink
	if err := os.Symlink(target, viaLink); err != nil {
		t.Fatal(err)
	}

	fePath := filepath.Join(root, "via_link.txt") // reached through the symlinked ancestor
	fileEdits := []lsp.FileEdit{
		{Path: fePath, Edits: []lsp.TextEdit{edit(0, 0, 4, "Z")}},
	}
	if _, err := applyFileEdits(fileEdits, root, "", "", true); err != nil {
		t.Fatalf("applyFileEdits: %v, want nil — an in-project symlink must not be refused merely because root's own ancestor is also a symlink", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Z" {
		t.Fatalf("target.txt = %q, want %q", got, "Z")
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

// resolvedTempDir is t.TempDir() with every symlink and short name expanded.
//
// GitHub's Windows runner sets TMP to the 8.3 SHORT form
// (C:\Users\RUNNER~1\...), which t.TempDir() inherits. Production code that
// canonicalises a path — anything reaching filepath.EvalSymlinks — returns the
// LONG form (C:\Users\runneradmin\...). Comparing a resolved result against an
// unresolved fixture root then fails while both strings name the same
// directory. Resolve the root once here so a test compares like with like,
// rather than weakening the comparison at the assertion.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	return resolvePath(t, t.TempDir())
}

// resolvePath is the resolution resolvedTempDir applies: EvalSymlinks, which
// on Windows also expands an 8.3 short-form path (C:\Users\RUNNER~1\...) to
// its long form — the specific behaviour the GitHub Windows runner's TMP
// forces this package to depend on. Split out from resolvedTempDir so a test
// can drive it against a path it did not get from t.TempDir() itself (see
// tempdir_windows_test.go).
func resolvePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve path %q: %v", path, err)
	}
	return resolved
}
