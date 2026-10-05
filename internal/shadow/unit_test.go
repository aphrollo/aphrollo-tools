package shadow

import (
	"os"
	"path/filepath"
	"testing"
)

// tree makes the files (empty) under a temp dir and returns the dir.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// manifestRoot stands in for the edit hook's marker walk: the nearest directory
// with a Cargo.toml or package.json.
func manifestRoot(file string) string {
	return findUpAny(filepath.Dir(file), "Cargo.toml", "package.json")
}

func findUpAny(dir string, names ...string) string {
	for _, n := range names {
		if d := findUp(dir, n); d != "" {
			return d
		}
	}
	return ""
}

func TestUnitOf_NamesAUnitPerLanguage(t *testing.T) {
	root := tree(t,
		".git/HEAD",
		"go.mod", "internal/lane/lane.go", "main.go",
		"tools/x/go.mod", "tools/x/pkg/p.go", "tools/x/x.go",
		"crates/engine/Cargo.toml", "crates/engine/src/lib.rs",
		"web/package.json", "web/src/app.ts",
		"scripts/run.py",
	)
	cases := []struct {
		name, file, id, kind string
		ok                   bool
	}{
		{"a go package is its directory under the module root", "internal/lane/lane.go", "internal/lane", unitGoPackage, true},
		{"the root package of a root module is dot", "main.go", ".", unitGoPackage, true},
		{"a module below the repository root keeps its path", "tools/x/pkg/p.go", "tools/x/pkg", unitGoPackage, true},
		{"the root package of a nested module is the module's path", "tools/x/x.go", "tools/x", unitGoPackage, true},
		{"a rust file is its crate", "crates/engine/src/lib.rs", "rust:crates/engine", unitProjectRoot, true},
		{"a node file is its package", "web/src/app.ts", "typescript:web", unitProjectRoot, true},
		{"a file in no project has no unit", "scripts/run.py", "", "", false},
	}
	for _, c := range cases {
		got, ok := UnitOf(filepath.Join(root, filepath.FromSlash(c.file)), manifestRoot)
		if ok != c.ok || got.ID != c.id || got.Kind != c.kind {
			t.Errorf("%s: UnitOf(%s) = %+v ok=%v, want id %q kind %q ok=%v", c.name, c.file, got, ok, c.id, c.kind, c.ok)
		}
	}
}

// A crate and a package at the same path, or two languages at the repository root,
// are two units: the id leads with the language row's name.
func TestUnitOf_ProjectRootUnitsOfTwoLanguagesAtOnePathDoNotCollide(t *testing.T) {
	root := tree(t, ".git/HEAD", "Cargo.toml", "package.json", "src/lib.rs", "src/app.ts")
	rs, _ := UnitOf(filepath.Join(root, "src", "lib.rs"), manifestRoot)
	ts, _ := UnitOf(filepath.Join(root, "src", "app.ts"), manifestRoot)
	if rs.ID == ts.ID || rs.ID != "rust:." || ts.ID != "typescript:." {
		t.Errorf("units = %q and %q, want rust:. and typescript:.", rs.ID, ts.ID)
	}
}

func TestUnitOf_ARepositoryOfNoGitDirNamesUnitsFromTheModuleItself(t *testing.T) {
	root := tree(t, "go.mod", "a/a.go")
	got, ok := UnitOf(filepath.Join(root, "a", "a.go"), manifestRoot)
	if !ok || got.ID != "a" || got.Project != "." {
		t.Errorf("UnitOf = %+v ok=%v, want unit a of project .", got, ok)
	}
}
