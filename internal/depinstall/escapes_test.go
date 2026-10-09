package depinstall

import (
	"os"
	"path/filepath"
	"testing"
)

// lanePkg makes <lane>/frontend/node_modules/<name>/index.js and returns the
// package directory.
func lanePkg(t *testing.T, lane, name string) string {
	t.Helper()
	dir := filepath.Join(lane, "frontend", NodeModules, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// replaceWithLink swaps the package directory pkg for a link to target, the
// way a donor install leaves one package resolving into another lane.
func replaceWithLink(t *testing.T, pkg, target string) {
	t.Helper()
	if err := os.RemoveAll(pkg); err != nil {
		t.Fatal(err)
	}
	if err := LinkDir(target, pkg); err != nil {
		t.Fatalf("a directory link could not be made: %v", err)
	}
}

// The merge bug: lane L4's node_modules held react resolving into lane L6, so
// a checkout linked to L4 loaded react from L6 and the rest from L4 and node
// saw two Reacts. The self-contained check must name that package.
func TestEscapes_APackageResolvingIntoAnotherLaneIsNamed(t *testing.T) {
	root := t.TempDir()
	l4, l6 := filepath.Join(root, "l4"), filepath.Join(root, "l6")
	lanePkg(t, l4, "zustand")
	replaceWithLink(t, lanePkg(t, l4, "react"), lanePkg(t, l6, "react"))

	got := Escapes(filepath.Join(l4, "frontend", NodeModules), l4)

	if len(got) != 1 || got[0].Name != "react" {
		t.Fatalf("Escapes = %+v, want exactly react", got)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(l6, "frontend", NodeModules, "react"))
	if got[0].Real != want {
		t.Errorf("react resolves to %q, want %q", got[0].Real, want)
	}
}

// A scoped package is one level down; a donor link there is the same mix.
func TestEscapes_AScopedPackageResolvingOutsideIsNamed(t *testing.T) {
	root := t.TempDir()
	l4, l6 := filepath.Join(root, "l4"), filepath.Join(root, "l6")
	replaceWithLink(t, lanePkg(t, l4, "@testing-library/react"), lanePkg(t, l6, "@testing-library/react"))

	got := Escapes(filepath.Join(l4, "frontend", NodeModules), l4)

	if len(got) != 1 || got[0].Name != "@testing-library/react" {
		t.Fatalf("Escapes = %+v, want @testing-library/react", got)
	}
}

// An install whose packages all stay in the lane (a link between two of its
// own packages included) is self-contained.
func TestEscapes_AnInstallInsideTheLaneIsClean(t *testing.T) {
	root := t.TempDir()
	l4 := filepath.Join(root, "l4")
	lanePkg(t, l4, "zustand")
	replaceWithLink(t, lanePkg(t, l4, "react-alias"), lanePkg(t, l4, "react"))

	if got := Escapes(filepath.Join(l4, "frontend", NodeModules), l4); len(got) != 0 {
		t.Fatalf("Escapes = %+v, want none", got)
	}
}

// A lane whose whole node_modules is a link into another lane (the shape
// `workspace create` leaves when a donor is junctioned in) escapes entirely.
func TestEscapes_AWholeNodeModulesLinkedIntoAnotherLaneIsNamed(t *testing.T) {
	root := t.TempDir()
	l4, l6 := filepath.Join(root, "l4"), filepath.Join(root, "l6")
	lanePkg(t, l6, "react")
	if err := os.MkdirAll(filepath.Join(l4, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := LinkDir(filepath.Join(l6, "frontend", NodeModules), filepath.Join(l4, "frontend", NodeModules)); err != nil {
		t.Fatalf("a directory link could not be made: %v", err)
	}

	got := Escapes(filepath.Join(l4, "frontend", NodeModules), l4)

	if len(got) != 1 || got[0].Name != NodeModules {
		t.Fatalf("Escapes = %+v, want node_modules itself", got)
	}
}
