package depinstall

import (
	"os"
	"path/filepath"
	"testing"
)

// sharedInstall writes a primary checkout's node_modules holding one package
// and returns the node_modules dir and the package file inside it.
func sharedInstall(t *testing.T) (nm, file string) {
	t.Helper()
	nm = filepath.Join(t.TempDir(), NodeModules)
	if err := os.MkdirAll(filepath.Join(nm, "fakepkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(nm, "fakepkg", "index.js")
	if err := os.WriteFile(file, []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return nm, file
}

// A lane whose node_modules links the primary's: removing the lane unlinks
// the link and leaves the primary's install whole.
func TestRemoveTree_KeepsTheTargetOfASymlinkedNodeModules(t *testing.T) {
	nm, file := sharedInstall(t)
	lane := filepath.Join(t.TempDir(), "lane")
	if err := os.MkdirAll(filepath.Join(lane, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nm, filepath.Join(lane, "web", NodeModules)); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTree(lane); err != nil {
		t.Fatalf("RemoveTree: %v", err)
	}
	if _, err := os.Lstat(lane); !os.IsNotExist(err) {
		t.Errorf("the lane must be gone, lstat err = %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the linked node_modules must keep its contents: %v", err)
	}
}

// RemoveLinks leaves every real file and directory in place: only the link
// goes, and the tree around it is untouched for the caller to remove.
func TestRemoveLinks_RemovesOnlyTheLinks(t *testing.T) {
	nm, _ := sharedInstall(t)
	lane := t.TempDir()
	own := filepath.Join(lane, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(lane, NodeModules)
	if err := os.Symlink(nm, link); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLinks(lane); err != nil {
		t.Fatalf("RemoveLinks: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("the link must be gone, lstat err = %v", err)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("a real file must stay: %v", err)
	}
}

// A path that is already gone needs nothing: removal stays idempotent.
func TestRemoveTree_MissingPathIsNoError(t *testing.T) {
	if err := RemoveTree(filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Fatalf("RemoveTree of a missing path: %v", err)
	}
}

// The Windows branch: Go reports a directory junction as ModeIrregular with
// no ModeDir, and git or an older os.RemoveAll can recurse through it into
// the primary's node_modules. RemoveLinks treats it as a link and unlinks it
// with os.Remove, which refuses a real non-empty directory, so a junction it
// cannot unlink stops the removal rather than being walked into.
func TestRemoveTree_AJunctionItCannotUnlinkStopsTheRemoval(t *testing.T) {
	defer TreatAsJunction(NodeModules)()
	lane := t.TempDir()
	file := filepath.Join(lane, NodeModules, "fakepkg", "index.js")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTree(lane); err == nil {
		t.Fatal("a junction that could not be unlinked must stop the removal")
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the junction's target must keep its contents: %v", err)
	}
}

// Every link kind the walk must unlink rather than descend into or delete
// through, and the kinds it must leave to the caller.
func TestIsLink_SymlinksAndJunctionsOnly(t *testing.T) {
	for _, c := range []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{"symlink", os.ModeSymlink, true},
		{"windows junction", os.ModeIrregular, true},
		{"directory", os.ModeDir, false},
		{"regular file", 0, false},
	} {
		if got := isLink(c.mode); got != c.want {
			t.Errorf("isLink(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
