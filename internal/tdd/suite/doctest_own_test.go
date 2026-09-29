package suite

import (
	"path/filepath"
	"reflect"
	"testing"
)

// These are suite's own tests of doctest.go: doctestRunners, packageDirs and
// packageHasDoctests are reached today only through internal/tdd/precommit's
// gate tests, so a mutant on one of their lines survives suite's own tests
// and the mutation gate can only file it SCOPE UNKNOWN.

// rustWorkspace lays down a two-member workspace: one crate with a doc-comment
// fence, one without, plus a target dir and a dot dir each holding a manifest
// that must never be picked up.
func rustWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\nmembers = [\"crates/documented\", \"crates/plain\"]\n")
	write(t, ws, "crates/documented/Cargo.toml", "[package]\nname = \"documented\"\nversion = \"0.1.0\"\n")
	write(t, ws, "crates/documented/src/lib.rs", "/// Adds.\n///\n/// ```\n/// assert_eq!(documented::add(1, 1), 2);\n/// ```\npub fn add(a: i32, b: i32) -> i32 { a + b }\n")
	write(t, ws, "crates/plain/Cargo.toml", "[package]\nname = \"plain\"\nversion = \"0.1.0\"\n")
	write(t, ws, "crates/plain/src/lib.rs", "pub fn one() -> i32 { 1 }\n")
	write(t, ws, "target/debug/build/ghost/Cargo.toml", "[package]\nname = \"ghost-target\"\nversion = \"0.1.0\"\n")
	write(t, ws, ".hidden/vendored/Cargo.toml", "[package]\nname = \"ghost-dot\"\nversion = \"0.1.0\"\n")
	return ws
}

// TestPackageDirs_MapsEveryNamedPackageToItsOwnDirectory pins the map: each
// package name resolves to the directory holding its manifest, and the
// virtual workspace manifest (no [package]) names nothing.
func TestPackageDirs_MapsEveryNamedPackageToItsOwnDirectory(t *testing.T) {
	t.Parallel()
	ws := rustWorkspace(t)
	got := packageDirs(ws)
	want := map[string]string{
		"documented": filepath.Join(ws, "crates", "documented"),
		"plain":      filepath.Join(ws, "crates", "plain"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("packageDirs = %v, want %v", got, want)
	}
}

// TestPackageDirs_SkipsTargetAndDotDirectories pins the walk's two skips: a
// manifest inside a target dir (a build's vendored copy) or a dot dir is not a
// member of the workspace.
func TestPackageDirs_SkipsTargetAndDotDirectories(t *testing.T) {
	t.Parallel()
	got := packageDirs(rustWorkspace(t))
	for _, ghost := range []string{"ghost-target", "ghost-dot"} {
		if _, found := got[ghost]; found {
			t.Fatalf("packageDirs = %v, want %s left out", got, ghost)
		}
	}
}

// TestPackageDirs_ARootThatIsItselfADotDirectoryIsStillWalked pins that the
// skip never applies to the walk root: a workspace checked out under a
// dot-named or target-named directory still enumerates its own members.
func TestPackageDirs_ARootThatIsItselfADotDirectoryIsStillWalked(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), ".ws")
	write(t, root, "Cargo.toml", "[package]\nname = \"rooted\"\nversion = \"0.1.0\"\n")
	if got := packageDirs(root); got["rooted"] != root {
		t.Fatalf("packageDirs = %v, want rooted at %s", got, root)
	}
}

// TestPackageHasDoctests_AFenceInAnOuterDocCommentCounts pins the positive
// case for `///`.
func TestPackageHasDoctests_AFenceInAnOuterDocCommentCounts(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(rustWorkspace(t), "crates", "documented")
	if !packageHasDoctests(dir) {
		t.Fatal("a crate whose src/ opens a fence inside a /// comment has doctests")
	}
}

// TestPackageHasDoctests_AFenceInAnInnerDocCommentCounts pins `//!`, the
// crate-level doc comment form.
func TestPackageHasDoctests_AFenceInAnInnerDocCommentCounts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "src/lib.rs", "//! Crate docs.\n//!\n//! ```\n//! let x = 1;\n//! ```\n")
	if !packageHasDoctests(dir) {
		t.Fatal("a fence inside a //! comment is a doctest")
	}
}

// TestPackageHasDoctests_AFenceOutsideADocCommentDoesNotCount pins that a
// fence in a string or an ordinary comment buys no cargo run.
func TestPackageHasDoctests_AFenceOutsideADocCommentDoesNotCount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "src/lib.rs", "// ```\nconst FENCE: &str = \"```\";\n")
	if packageHasDoctests(dir) {
		t.Fatal("a fence in a plain comment or a string is not a doctest")
	}
}

// TestPackageHasDoctests_ADocCommentWithoutAFenceDoesNotCount pins that prose
// in a doc comment is not a doctest.
func TestPackageHasDoctests_ADocCommentWithoutAFenceDoesNotCount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "src/lib.rs", "/// Just prose.\npub fn f() {}\n")
	if packageHasDoctests(dir) {
		t.Fatal("a doc comment with no fence is not a doctest")
	}
}

// TestPackageHasDoctests_OnlyRustFilesAreRead pins the extension filter: a
// fence in a markdown or text file under src/ is not a doctest.
func TestPackageHasDoctests_OnlyRustFilesAreRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "src/notes.md", "/// ```\n")
	if packageHasDoctests(dir) {
		t.Fatal("only .rs files under src/ can carry a doctest")
	}
}

// TestPackageHasDoctests_FindsAFenceInANestedModule pins the recursion into
// src/'s subdirectories.
func TestPackageHasDoctests_FindsAFenceInANestedModule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "src/lib.rs", "pub mod deep;\n")
	write(t, dir, "src/deep/mod.rs", "/// ```\n/// let y = 2;\n/// ```\npub fn g() {}\n")
	if !packageHasDoctests(dir) {
		t.Fatal("a fence in src/deep/mod.rs is a doctest of the crate")
	}
}

// TestPackageHasDoctests_OnlySrcIsWalked pins the scope: a fence in tests/ or
// examples/ is not under src/, so it is not a doctest.
func TestPackageHasDoctests_OnlySrcIsWalked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "tests/it.rs", "/// ```\n/// x\n/// ```\n")
	if packageHasDoctests(dir) {
		t.Fatal("only src/ can carry doctests")
	}
}

// TestDoctestRunners_OneDocRunPerTouchedPackageThatHasDoctests pins the whole
// selection: a touched crate with a fence gets `cargo test -p <pkg> --doc` from
// the workspace root, one without gets nothing, and a name that is not a member
// gets nothing.
func TestDoctestRunners_OneDocRunPerTouchedPackageThatHasDoctests(t *testing.T) {
	t.Parallel()
	ws := rustWorkspace(t)
	got := doctestRunners(ws, []string{"documented", "plain", "stranger"})
	want := []Runner{{Cmd: "cargo", Args: []string{"test", "-p", "documented", "--doc"}, Dir: ws}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("doctestRunners = %+v, want %+v", got, want)
	}
}

// TestDoctestRunners_KeepsTheTouchedOrder pins that runs come out in the order
// the packages were named.
func TestDoctestRunners_KeepsTheTouchedOrder(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	for _, n := range []string{"zeta", "alpha"} {
		write(t, ws, "crates/"+n+"/Cargo.toml", "[package]\nname = \""+n+"\"\nversion = \"0.1.0\"\n")
		write(t, ws, "crates/"+n+"/src/lib.rs", "//! ```\n//! x\n//! ```\n")
	}
	got := doctestRunners(ws, []string{"zeta", "alpha"})
	if len(got) != 2 || got[0].Args[2] != "zeta" || got[1].Args[2] != "alpha" {
		t.Fatalf("doctestRunners = %+v, want zeta then alpha", got)
	}
}

// TestDoctestRunners_NothingTouchedRunsNothing pins the empty selection.
func TestDoctestRunners_NothingTouchedRunsNothing(t *testing.T) {
	t.Parallel()
	if got := doctestRunners(rustWorkspace(t), nil); len(got) != 0 {
		t.Fatalf("doctestRunners = %+v, want none", got)
	}
}

// TestPackageDirs_AWorkspaceRootThatDoesNotExistHasNoPackages pins the walk's
// error arm: nothing to read is an empty map, not a panic and not a stale one.
func TestPackageDirs_AWorkspaceRootThatDoesNotExistHasNoPackages(t *testing.T) {
	t.Parallel()
	if got := packageDirs(filepath.Join(t.TempDir(), "absent")); len(got) != 0 {
		t.Fatalf("packageDirs = %v, want none", got)
	}
}
