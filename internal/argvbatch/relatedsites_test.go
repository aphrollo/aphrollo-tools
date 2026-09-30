package argvbatch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// relatedRunnerSites is every non-test function in the gate that names a
// related-tests verb (vitest's `related`, jest's `--findRelatedTests`) as a
// string, keyed "<file>:<function>". Such a function either builds a
// command line that carries a changed-path list or reads one, and says which
// bounds it:
//
//	"bounded ..."  the line holds at most one path, or falls back to the
//	               full suite past the budget
//	"reads ..."    the function only recognises the verb in a command
//
// The exec call-site guard (callsites_test.go) sees a spread slice handed to
// a process; a related-tests runner is built from a list in one function and
// started in another, so it never shows there (issue #1008: the merge gate
// built a 330-path line and the executor started it as it stood). A new
// builder is not in this table, so the test fails on it: hold it to the
// budget and add it with what bounds it.
var relatedRunnerSites = map[string]string{
	"internal/tdd/suite/runner.go:narrowSourceEdit":              "bounded: one edited file",
	"internal/tdd/suite/runner_scope.go:narrowToStagedUnbounded": "bounded: narrowToStaged falls back to the full suite past the budget, and the executor's relatedWithinBudget holds whatever line still starts",
	"internal/tdd/suite/runscope.go:npmRunScope":                 "reads: recognises a related run in a command",
	"internal/tdd/suite/runsuite_related.go:relatedFullSuite":    "reads: rewrites a related run to the tool's full suite",
}

func relatedVerbFuncs(file *ast.File) []string {
	var out []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		hit := false
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && (s == "related" || s == "--findRelatedTests") {
				hit = true
			}
			return true
		})
		if hit {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

func TestRelatedRunnerSites_EveryBuilderOfARelatedRunIsBounded(t *testing.T) {
	repo := filepath.Join("..", "..") // tree-read-ok: the guard reads the tool's own source, whatever the tree under test
	root := filepath.Join(repo, "internal", "tdd")
	found := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), "gotmp") || d.Name() == "tddtest" {
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
		rel, _ := filepath.Rel(repo, path)
		for _, fn := range relatedVerbFuncs(file) {
			found[filepath.ToSlash(rel)+":"+fn] = true
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
		if _, ok := relatedRunnerSites[k]; !ok {
			t.Errorf("%s names a related-tests verb and is not accounted for: hold the line it builds to the budget (full suite past it) and add it to relatedRunnerSites with what bounds it", k)
		}
	}
	for k, why := range relatedRunnerSites {
		if !found[k] {
			t.Errorf("relatedRunnerSites has %s (%s), which no longer names a related-tests verb: remove the row", k, why)
		}
		if !strings.HasPrefix(why, "bounded") && !strings.HasPrefix(why, "reads") {
			t.Errorf("%s: %q must start with bounded or reads", k, why)
		}
	}
}

// TestRelatedVerbFuncs_FindsTheVerbInAFunctionAndNothingElse pins the guard's
// eye: the verb as a string literal in a function is a hit; a comment, an
// identifier and a longer word are not.
func TestRelatedVerbFuncs_FindsTheVerbInAFunctionAndNothingElse(t *testing.T) {
	src := `package p
func vitest() []string { return []string{"vitest", "related"} }
func jest() string { return "--findRelatedTests" }
func comment() { /* "related" */ }
func longer() string { return "relatedness" }
func ident() { related := 1; _ = related }
var pkg = "related"
`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(relatedVerbFuncs(file), ",")
	if want := "vitest,jest"; got != want {
		t.Fatalf("relatedVerbFuncs = %s, want %s", got, want)
	}
}
