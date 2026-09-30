package gitiso

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every package of the module that has tests runs them through a TestMain that
// isolates the git world: gitiso.Main or gitiso.Isolate, or tddtest.Main, which
// calls Isolate. A package without one runs its fixtures' bare git calls
// against whatever repository and global config the box hands the test binary
// (#1043), and no other check sees it: the world-leak law excuses a package
// that has any TestMain at all.
func TestModule_EveryPackageWithTestsIsolatesItsGitWorldInTestMain(t *testing.T) {
	// tree-read-ok: the guard is about the module's own test files.
	root := filepath.Join("..", "..")
	isolated := map[string]bool{}
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
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		ok, err := testMainIsolates(path)
		if err != nil {
			return err
		}
		isolated[dir] = isolated[dir] || ok
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(isolated) < 20 {
		t.Fatalf("the walk found tests in %d directories; it did not start at the module root", len(isolated))
	}
	var missing []string
	for dir, ok := range isolated {
		if !ok {
			missing = append(missing, dir)
		}
	}
	sort.Strings(missing)
	for _, dir := range missing {
		t.Errorf("%s has tests but no TestMain calling gitiso.Main, gitiso.Isolate, gitiso.MustIsolate or tddtest.Main", dir)
	}
}

// skipDir names the directories the walk leaves alone: fixtures, other
// checkouts and build output, none of which is a package of this module.
func skipDir(name string) bool {
	switch name {
	case "testdata", ".ratchet", ".git", ".worktrees", ".mutants", "target", "node_modules", "vendor":
		return true
	}
	return strings.HasPrefix(name, "gotmp")
}

// testMainIsolates reports whether the Go file at path declares TestMain and
// calls an isolating entry point inside it. The qualified spellings count
// everywhere; the unqualified one only inside the package that declares it,
// since a package cannot import itself.
func testMainIsolates(path string) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	pkg := f.Name.Name
	found := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				x, isIdent := f.X.(*ast.Ident)
				if isIdent && ((x.Name == "gitiso" && (f.Sel.Name == "Main" || f.Sel.Name == "Isolate" || f.Sel.Name == "MustIsolate")) ||
					(x.Name == "tddtest" && f.Sel.Name == "Main")) {
					found = true
				}
			case *ast.Ident:
				if (pkg == "gitiso" && (f.Name == "Main" || f.Name == "Isolate" || f.Name == "MustIsolate")) || (pkg == "tddtest" && f.Name == "Main") {
					found = true
				}
			}
			return true
		})
	}
	return found, nil
}

// A TestMain that only mentions the entry point, or a package that has one
// without calling it, is not isolated.
func TestTestMainIsolates_NeedsTheCallInsideTestMainItself(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		src  string
		want bool
	}{
		"calls_test.go":     {"package p\nfunc TestMain(m *testing.M) { os.Exit(gitiso.Main(func() int { return m.Run() })) }\n", true},
		"isolate_test.go":   {"package p\nfunc TestMain(m *testing.M) { gitiso.Isolate(dir) }\n", true},
		"tddtest_test.go":   {"package p\nfunc TestMain(m *testing.M) { tddtest.Main(m, s) }\n", true},
		"outside_test.go":   {"package p\nfunc helper() { gitiso.Main(nil) }\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n", false},
		"other_test.go":     {"package p\nfunc TestMain(m *testing.M) { other.Main(m) }\n", false},
		"self_test.go":      {"package gitiso\nfunc TestMain(m *testing.M) { Main(nil) }\n", true},
		"selfiso_test.go":   {"package gitiso\nfunc TestMain(m *testing.M) { Isolate(dir) }\n", true},
		"selfmust_test.go":  {"package gitiso\nfunc TestMain(m *testing.M) { MustIsolate(dir) }\n", true},
		"must_test.go":      {"package p\nfunc TestMain(m *testing.M) { gitiso.MustIsolate(dir) }\n", true},
		"selfother_test.go": {"package p\nfunc TestMain(m *testing.M) { Main(nil) }\n", false},
		"selft_test.go":     {"package tddtest\nfunc TestMain(m *testing.M) { Main(m, s) }\n", true},
		"selftiso_test.go":  {"package tddtest\nfunc TestMain(m *testing.M) { Isolate(dir) }\n", false},
		"method_test.go":    {"package p\nfunc (r R) TestMain(m *testing.M) { gitiso.Main(nil) }\n", false},
		"nobody_test.go":    {"package p\nfunc TestMain(m *testing.M)\n", false},
		"isolonly_test.go":  {"package p\nfunc TestMain(m *testing.M) { gitiso.Other(dir) }\n", false},
		"tddother_test.go":  {"package p\nfunc TestMain(m *testing.M) { tddtest.Other(m) }\n", false},
	}
	for name, c := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := testMainIsolates(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != c.want {
			t.Errorf("%s: isolated = %v, want %v", name, got, c.want)
		}
	}
}

func TestSkipDir_NamesTheDirectoriesThatAreNotPackagesOfTheModule(t *testing.T) {
	for name, want := range map[string]bool{
		"testdata": true, ".ratchet": true, ".git": true, ".worktrees": true, ".mutants": true,
		"target": true, "node_modules": true, "vendor": true, "gotmp": true, "gotmp-x": true,
		"internal": false, "gitiso": false, "tmp": false, "targets": false, "xgotmp": false,
	} {
		if got := skipDir(name); got != want {
			t.Errorf("skipDir(%q) = %v, want %v", name, got, want)
		}
	}
}
