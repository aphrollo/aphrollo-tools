package argvbatch

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

// loopBuiltArgSites is every non-test function that grows an argument list
// one element at a time in a loop (`args = append(args, f)`) and hands it to a
// process, keyed "<file>:<function>". The exec call-site guard
// (callsites_test.go) sees a spread slice; a list built in a loop never shows
// there (issue #996: the pytest fail-first proof put every staged test file
// on one line, and the guard was blind to it). Each row says what bounds the
// line:
//
//	"bounded ..."  the line is held to the budget, or falls back to a
//	               whole-suite run past it, or its elements are a fixed set
//	"batched ..."  the executor splits the line before it starts
var loopBuiltArgSites = map[string]string{
	"internal/tdd/mutation/mutants_prove_widen.go:widenGoSelection": "batched: the go test package list it builds is split by SplitCommand where the mutation tool starts it",
	"internal/tdd/suite/runner_pytest.go:narrowPytestFailFirst":     "bounded: past stagedArgvBudget the runner stays whole instead of naming every staged test file",
}

func isRunnerOrExec(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.CompositeLit:
		id, ok := x.Type.(*ast.Ident)
		return ok && id.Name == "Runner"
	case *ast.CallExpr:
		return isExecCall(x)
	}
	return false
}

// loopBuiltArgFuncs is the names of the functions in file that append single
// elements to an argument-named slice inside a loop and also build a Runner
// or call a process-starting helper.
func loopBuiltArgFuncs(file *ast.File) []string {
	var out []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		loops, spawns := false, false
		ast.Inspect(fn, func(n ast.Node) bool {
			if isRunnerOrExec(n) {
				spawns = true
			}
			var body *ast.BlockStmt
			switch l := n.(type) {
			case *ast.RangeStmt:
				body = l.Body
			case *ast.ForStmt:
				body = l.Body
			}
			if body == nil {
				return true
			}
			ast.Inspect(body, func(m ast.Node) bool {
				as, ok := m.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
					return true
				}
				call, ok := as.Rhs[0].(*ast.CallExpr)
				if !ok || call.Ellipsis != token.NoPos || len(call.Args) != 2 {
					return true
				}
				if f, ok := call.Fun.(*ast.Ident); !ok || f.Name != "append" {
					return true
				}
				if lhs, ok := as.Lhs[0].(*ast.Ident); ok {
					name := strings.ToLower(lhs.Name)
					if name == "args" || name == "argv" || strings.HasSuffix(name, "args") {
						loops = true
					}
				}
				return true
			})
			return true
		})
		if loops && spawns {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

func TestLoopBuiltArgSites_EveryArgumentListGrownInALoopIsBounded(t *testing.T) {
	root := filepath.Join("..", "..") // tree-read-ok: the guard reads the tool's own source, whatever the tree under test
	found := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case d.Name() == ".git", d.Name() == ".worktrees", strings.HasPrefix(d.Name(), "gotmp"), rel == "internal/tdd/internal/tddtest":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, fn := range loopBuiltArgFuncs(file) {
			found[rel+":"+fn] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := loopBuiltArgSites[k]; !ok {
			t.Errorf("%s grows an argument list in a loop and starts a process and is not accounted for: hold its line to the budget (stagedArgvBudget) or split it through argvbatch, then add it to loopBuiltArgSites with what bounds it", k)
		}
	}
	for k, why := range loopBuiltArgSites {
		if !found[k] {
			t.Errorf("loopBuiltArgSites has %s (%s), which no longer grows an argument list in a loop: remove the row", k, why)
		}
		if !strings.HasPrefix(why, "bounded") && !strings.HasPrefix(why, "batched") {
			t.Errorf("%s: %q must start with bounded or batched", k, why)
		}
	}
}

// TestLoopBuiltArgFuncs_FindsAListGrownInALoopAndNothingElse pins the guard's
// eye: a single-element append to an argument-named slice in a loop, in a
// function that builds a Runner or calls exec, is a hit; a spread, a
// differently named slice, a loop-free append and a function that starts
// nothing are not.
func TestLoopBuiltArgFuncs_FindsAListGrownInALoopAndNothingElse(t *testing.T) {
	src := `package p
func viaRange(files []string) Runner {
	args := []string{"-q"}
	for _, f := range files { args = append(args, f) }
	return Runner{Cmd: "pytest", Args: args}
}
func viaFor(files []string) { argv := []string{}; for i := 0; i < len(files); i++ { argv = append(argv, files[i]) }; exec.Command("x", argv...) }
func spread(files []string) Runner { args := []string{}; for range files { args = append(args, files...) }; return Runner{Args: args} }
func otherName(files []string) Runner { var out []string; for _, f := range files { out = append(out, f) }; return Runner{Args: out} }
func noLoop(f string) Runner { args := []string{}; args = append(args, f); return Runner{Args: args} }
func noSpawn(files []string) []string { var args []string; for _, f := range files { args = append(args, f) }; return args }
`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(loopBuiltArgFuncs(file), ",")
	if want := "viaRange,viaFor"; got != want {
		t.Fatalf("loopBuiltArgFuncs = %s, want %s", got, want)
	}
}
