package tdd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every package under internal/tdd runs its tests through tddtest.Main, the
// one TestMain body that puts up the nets: an isolated CLAUDE_CONFIG_DIR and
// CARGO_HOME, a temp lock dir with the live one guarded, the gh stub on PATH,
// the git queue marker, the golden fixtures, the CI-runner probe. The
// test_world_leak law cannot see a package that skipped it: the law excuses a
// leak when a package declares ANY TestMain, not one that calls Main. A
// package carved out of internal/tdd with its own hand-written TestMain would
// run against the operator's real state and still read clean there.
func TestTDDPackages_EachRunsTestMainThroughTddtestMain(t *testing.T) {
	calls := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		ok, err := testMainCallsTddtestMain(path)
		if err != nil {
			return err
		}
		calls[dir] = calls[dir] || ok
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !calls["."] {
		t.Errorf("internal/tdd itself has no TestMain calling tddtest.Main")
	}
	var missing []string
	for dir, ok := range calls {
		if !ok {
			missing = append(missing, dir)
		}
	}
	sort.Strings(missing)
	for _, dir := range missing {
		t.Errorf("internal/tdd/%s has tests but no TestMain calling tddtest.Main", dir)
	}
}

// testMainCallsTddtestMain reports whether the Go file at path declares
// TestMain and calls tddtest.Main inside it.
func testMainCallsTddtestMain(path string) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" || fn.Body == nil {
			continue
		}
		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "tddtest" && sel.Sel.Name == "Main" {
				found = true
			}
			return !found
		})
		return found, nil
	}
	return false, nil
}
