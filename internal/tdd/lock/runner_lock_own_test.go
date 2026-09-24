package lock

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCargoTomlHasWorkspaceTable_ToleratesTrailingCommentOrWhitespace pins
// the review-2026-08-15 fix: a manifest with "[workspace]  # root" or
// "[workspace] " (trailing whitespace, no comment) must still be recognised,
// not just a byte-exact "[workspace]" line — the earlier bug silently fell
// back to "no workspace found here", so a member crate ran unscoped from its
// own directory instead of the resolved workspace root.
func TestCargoTomlHasWorkspaceTable_ToleratesTrailingCommentOrWhitespace(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"exact", "[workspace]\nmembers = [\"a\"]\n", true},
		{"trailing comment", "[workspace]  # root\n", true},
		{"trailing whitespace", "[workspace]   \n", true},
		{"different table", "[package]\nname = \"m\"\n", false},
		{"workspace as a value not a header", "foo = \"[workspace]\"\n", false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		manifest := filepath.Join(dir, "Cargo.toml")
		if err := os.WriteFile(manifest, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := cargoTomlHasWorkspaceTable(manifest); got != c.want {
			t.Errorf("%s: cargoTomlHasWorkspaceTable(%q) = %v, want %v", c.name, c.body, got, c.want)
		}
	}
}

// TestCargoTomlHasWorkspaceTable_MissingManifestIsFalse is the fallback the
// caller (cargoWorkspaceRoot) relies on to keep walking up: an unreadable or
// absent Cargo.toml must never be mistaken for a positive match.
func TestCargoTomlHasWorkspaceTable_MissingManifestIsFalse(t *testing.T) {
	dir := t.TempDir()
	if got := cargoTomlHasWorkspaceTable(filepath.Join(dir, "Cargo.toml")); got {
		t.Fatal("cargoTomlHasWorkspaceTable on a missing manifest = true, want false")
	}
}

// TestCargoWorkspaceRoot_WalksUpToTheDeclaringAncestor pins the resolution a
// member crate needs: its own Cargo.toml has no [workspace] table, but an
// ancestor's does, and that ancestor — not the member's own directory — is
// where the checked-in .config/nextest.toml and Cargo.lock live.
func TestCargoWorkspaceRoot_WalksUpToTheDeclaringAncestor(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "crates", "server")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "Cargo.toml"), "[workspace]\nmembers = [\"crates/*\"]\n")
	write(filepath.Join(member, "Cargo.toml"), "[package]\nname = \"server\"\n")

	if got := cargoWorkspaceRoot(member); got != root {
		t.Fatalf("cargoWorkspaceRoot(%q) = %q, want the workspace root %q", member, got, root)
	}
}

// TestCargoWorkspaceRoot_NoWorkspaceFallsBackToItsOwnRoot is the other rule:
// a crate with no encompassing [workspace] anywhere above it (up to the
// filesystem root) is its own "workspace root" rather than an error.
func TestCargoWorkspaceRoot_NoWorkspaceFallsBackToItsOwnRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \"solo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cargoWorkspaceRoot(root); got != root {
		t.Fatalf("cargoWorkspaceRoot(%q) = %q, want root unchanged when no ancestor declares a workspace", root, got)
	}
}
