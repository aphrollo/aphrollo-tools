package gitiso

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// age backdates every entry of dir, the way a run killed long ago left it.
func age(t *testing.T, path string, by time.Duration) {
	t.Helper()
	old := time.Now().Add(-by)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// A test binary that was killed never reaches its own removal. The next
// binary of the same family finds the leftover directory in the temp dir and
// removes it, so a box's temp dir does not fill up one killed run at a time.
func TestMkRoot_RemovesAStaleDirOfItsOwnFamilyAndKeepsTheRest(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	mk := func(name string, by time.Duration) string {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Join(p, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		age(t, p, by)
		return p
	}
	stale := mk("fam-stale", 3*time.Hour)
	fresh := mk("fam-fresh", time.Minute)
	other := mk("other-stale", 3*time.Hour)
	file := filepath.Join(tmp, "fam-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	age(t, file, 3*time.Hour)

	root, err := MkRoot("fam-")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(root, filepath.Join(tmp, "fam-")) {
		t.Errorf("root %q is not under the temp dir with the family prefix", root)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Errorf("stale dir %s of the same family survived", stale)
	}
	for _, keep := range []string{fresh, other, file} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s must stay: %v", keep, err)
		}
	}
}

// Go's module cache, and a test that took a directory read-only, leave trees
// os.RemoveAll cannot unlink from.
func TestRemoveAll_RemovesATreeWithReadOnlyDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tree")
	deep := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "f"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{deep, filepath.Join(dir, "a"), dir} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}

	RemoveAll(dir)

	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("%s is still there", dir)
	}
}

// Every package's TestMain makes its root through MkRoot, which sweeps what a
// killed run left; a bare os.MkdirTemp there is a root nothing ever sweeps.
func TestModule_NoTestMainMakesItsRootWithABareMkdirTemp(t *testing.T) {
	// tree-read-ok: the guard is about the module's own test files.
	root := filepath.Join("..", "..")
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		slash := filepath.ToSlash(path)
		if !strings.HasSuffix(path, "_test.go") && !strings.HasSuffix(slash, "tddtest/main.go") {
			return nil
		}
		hits, err := bareRootMakers(path)
		for _, h := range hits {
			bad = append(bad, slash+": "+h)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("%s: make the root with gitiso.MkRoot and remove it with gitiso.RemoveAll", b)
	}
}

// bareRootMakers names every os.MkdirTemp call made directly in a TestMain or
// in a function called Main, outside any closure: the run's root, made before
// the temp dir is walled off.
func bareRootMakers(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var hits []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || (fn.Name.Name != "TestMain" && fn.Name.Name != "Main") {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if _, closure := n.(*ast.FuncLit); closure {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "MkdirTemp" {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "os" {
					hits = append(hits, fn.Name.Name+" calls os.MkdirTemp")
				}
			}
			return true
		})
	}
	return hits, nil
}
