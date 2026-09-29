package suite

import "testing"

// These are suite's own tests of edit_ownership.go's unownedEdit, reached today
// only through internal/tdd/postedit.

// TestUnownedEdit_ACargoFileOutsideEveryMemberOfAWorkspaceIsUnowned pins the
// cargo arm: a .rs file no [package] owns, under a real workspace manifest,
// belongs to no crate.
func TestUnownedEdit_ACargoFileOutsideEveryMemberOfAWorkspaceIsUnowned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	write(t, root, "crates/a/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	if got := unownedEdit(Runner{Cmd: "cargo"}, root+"/scratch/x.rs", root); got != "crate" {
		t.Fatalf("unownedEdit = %q, want crate", got)
	}
}

// TestUnownedEdit_ACargoFileInsideAMemberIsOwned pins the negative.
func TestUnownedEdit_ACargoFileInsideAMemberIsOwned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	write(t, root, "crates/a/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	if got := unownedEdit(Runner{Cmd: "cargo"}, root+"/crates/a/src/lib.rs", root); got != "" {
		t.Fatalf("unownedEdit = %q, want owned", got)
	}
}

// TestUnownedEdit_WithoutAWorkspaceTableNothingProvesTheFileOutside pins that
// a manifest with no [workspace] table proves nothing.
func TestUnownedEdit_WithoutAWorkspaceTableNothingProvesTheFileOutside(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"solo\"\nversion = \"0.1.0\"\n")
	if got := unownedEdit(Runner{Cmd: "cargo"}, root+"/scratch/x.rs", root); got != "" {
		t.Fatalf("unownedEdit = %q, want empty", got)
	}
}

// TestUnownedEdit_ABuildScriptIsNeverUnowned pins the build.rs exemption.
func TestUnownedEdit_ABuildScriptIsNeverUnowned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = []\n")
	if got := unownedEdit(Runner{Cmd: "cargo"}, root+"/scratch/build.rs", root); got != "" {
		t.Fatalf("unownedEdit = %q, want empty for build.rs", got)
	}
}

// TestUnownedEdit_ANonCodeFileIsNeverUnowned pins the code-file guard.
func TestUnownedEdit_ANonCodeFileIsNeverUnowned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = []\n")
	if got := unownedEdit(Runner{Cmd: "cargo"}, root+"/docs/notes.md", root); got != "" {
		t.Fatalf("unownedEdit = %q, want empty for prose", got)
	}
}

// TestUnownedEdit_AGoFileAlwaysMakesItsOwnPackage pins the go arm's exemption:
// a .go file is a package of its own wherever it sits.
func TestUnownedEdit_AGoFileAlwaysMakesItsOwnPackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	if got := unownedEdit(Runner{Cmd: "go"}, root+"/newpkg/x.go", root); got != "" {
		t.Fatalf("unownedEdit = %q, want empty for a .go file", got)
	}
}

// TestUnownedEdit_ANonGoCodeFileInADirWithNoGoFilesIsUnowned pins the go arm's
// positive case: a code file that is not Go, in a directory no Go package
// occupies, is outside every package.
func TestUnownedEdit_ANonGoCodeFileInADirWithNoGoFilesIsUnowned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "web/app.ts", "")
	if got := unownedEdit(Runner{Cmd: "go"}, root+"/web/app.ts", root); got != "Go package" {
		t.Fatalf("unownedEdit = %q, want Go package", got)
	}
}

// TestUnownedEdit_ANonGoCodeFileBesideGoFilesIsOwned pins the negative: the
// directory holds a Go package, so the file is beside one.
func TestUnownedEdit_ANonGoCodeFileBesideGoFilesIsOwned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "pkg/a.go", "package pkg\n")
	write(t, root, "pkg/embedded.ts", "")
	if got := unownedEdit(Runner{Cmd: "go"}, root+"/pkg/embedded.ts", root); got != "" {
		t.Fatalf("unownedEdit = %q, want owned", got)
	}
}

// TestUnownedEdit_OtherRunnersOwnEverything pins that only cargo and go judge
// ownership.
func TestUnownedEdit_OtherRunnersOwnEverything(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if got := unownedEdit(Runner{Cmd: "pytest"}, root+"/x.py", root); got != "" {
		t.Fatalf("unownedEdit = %q, want empty for pytest", got)
	}
}
