package tdd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// fuzzTargetsOnDisk is every top-level `func FuzzX(f *testing.F)` in the repo,
// as package dir (slash form, repo-relative) to target names. Parsed, not
// grepped, so a Fuzz func inside a fixture string is not a target.
func fuzzTargetsOnDisk(t *testing.T, root string) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "gotmp" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(src), "func Fuzz") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		rel = filepath.ToSlash(rel)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Fuzz") && fn.Type.Params.NumFields() == 1 {
				found[rel] = append(found[rel], fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

var fuzzMapRow = regexp.MustCompile(`\["([^"]+)"\]="([^"]*)"`)

// TestNightlyFuzzWorkflow_ListsEveryFuzzTarget fails, naming it, for a fuzz
// target in the repo that the workflow's target map does not run: a new
// target can never be forgotten, and the nightly run is the only place a
// fuzz target runs past its seed corpus.
func TestNightlyFuzzWorkflow_ListsEveryFuzzTarget(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "nightly-fuzz.yml")
	listed := map[string]map[string]bool{}
	for _, m := range fuzzMapRow.FindAllStringSubmatch(wf, -1) {
		listed[m[1]] = map[string]bool{}
		for _, n := range strings.Fields(m[2]) {
			listed[m[1]][n] = true
		}
	}
	var missing []string
	for pkg, names := range fuzzTargetsOnDisk(t, repoRootForTest(t)) {
		for _, n := range names {
			if !listed[pkg][n] {
				missing = append(missing, n+" in "+pkg)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("nightly-fuzz.yml's target map does not list: %s", strings.Join(missing, ", "))
	}
}
