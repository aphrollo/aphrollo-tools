package mutation

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CoverQuery is the answer of CoveringTests.
type CoverQuery struct {
	// Tests is the runner tests of the package to run for the edit, sorted:
	// the ones whose entry covers an edited function, and every test the store
	// cannot speak for. Empty when the store is not fresh.
	Tests []string
	// Total is how many runner tests the package has.
	Total int
	// Fresh says the store can pick the tests. When false the caller runs the
	// whole package, and Reason says why.
	Fresh  bool
	Reason string
}

// CoveringTests asks the kept coverage store of the package dir (relative to
// root) which of its tests cover the edited functions (`Name` or `Recv.Name`,
// as the store keys them). It reads the store the commit-time mutation flow
// keeps and never builds coverage itself: a package without a store, with a
// store measured under other build inputs, or with an entry that no longer
// holds anywhere but in the edited functions, is not fresh, and so is an edited
// function no entry names, or anything that cannot be read. Doubt is not
// fresh: the caller runs everything.
func CoveringTests(root, dir string, edited []string) CoverQuery {
	cfg, err := ReadMutantsConfig(root)
	if err != nil {
		return CoverQuery{Reason: fmt.Sprintf("the mutation config could not be read: %v", err)}
	}
	scan, err := scanPackage(filepath.Join(root, filepath.FromSlash(dir)), cfg.TestTags)
	if err != nil {
		return CoverQuery{Reason: fmt.Sprintf("reading the sources of %s: %v", dir, err)}
	}
	q := CoverQuery{Total: len(scan.TestKey)}
	notFresh := func(format string, args ...any) CoverQuery {
		q.Reason = fmt.Sprintf(format, args...)
		return q
	}
	if len(edited) == 0 {
		return notFresh("no function was named")
	}
	ctx := withTestTags(context.Background(), cfg.TestTags)
	env, err := coverEnvKey(ctx, root, dir, cfg)
	if err != nil {
		return notFresh("the build inputs could not be read: %v", err)
	}
	path := covStorePath(root, dir, env)
	if path == "" {
		return notFresh("no coverage store: this directory has no git dir to keep one in")
	}
	if _, err := os.Stat(path); err != nil {
		// A store of the same package under other build inputs is a different
		// answer than none at all.
		if i := strings.LastIndex(path, "-v3-"); i > 0 {
			if others, _ := filepath.Glob(path[:i] + "-v3-*.json"); len(others) > 0 {
				return notFresh("the coverage store of %s was measured under other build inputs (the build inputs differ)", dir)
			}
		}
		return notFresh("no coverage store for %s yet", dir)
	}
	st := loadCovStore(root, dir, env)
	if len(st.Tests) == 0 && len(st.Silent) == 0 {
		return notFresh("the coverage store of %s holds no test or does not read", dir)
	}
	if st.Rest != scan.Rest {
		return notFresh("a declaration that is not a function changed since the coverage was measured")
	}
	globals, aux := map[string]string{}, map[string]string{}
	runners := map[string]bool{}
	for _, key := range scan.TestKey {
		runners[key] = true
	}
	for key, f := range scan.Funcs {
		switch {
		case f.Global:
			globals[key] = f.Hash
		case f.Test && !runners[key]:
			aux[key] = f.Hash
		}
	}
	for _, v := range scan.Vars {
		if v.Test {
			aux[v.Key] = v.Hash
		}
	}
	if !maps2Equal(st.Globals, globals) {
		return notFresh("TestMain or an init function changed since the coverage was measured")
	}
	for key, old := range st.Aux {
		if now, ok := aux[key]; !ok || now != old {
			return notFresh("the test helper %s changed since the coverage was measured", key)
		}
	}

	covering := map[string]bool{}
	named := map[string]bool{}
	for name, key := range scan.TestKey {
		entry, ok := st.Tests[name]
		if !ok || entry.Hash != scan.Funcs[key].Hash {
			continue // joins the selection below
		}
		for _, c := range entry.Cover {
			if slices.Contains(edited, c.Func) {
				covering[name] = true
				named[c.Func] = true
				continue
			}
			if d, ok := scan.decl(c.Func); !ok || d.Hash != c.Hash {
				return notFresh("the coverage of %s no longer holds: %s changed besides the edit", name, c.Func)
			}
		}
	}
	for _, f := range edited {
		if !named[f] {
			return notFresh("no test covers %s in the store", f)
		}
	}
	unknown := scan.reexecTests()
	for name, key := range scan.TestKey {
		entry, ok := st.Tests[name]
		_, silent := st.Silent[name]
		if !ok || silent || entry.Hash != scan.Funcs[key].Hash {
			unknown = append(unknown, name)
		}
	}
	for name := range covering {
		q.Tests = append(q.Tests, name)
	}
	q.Tests = append(q.Tests, unknown...)
	slices.Sort(q.Tests)
	q.Tests = slices.Compact(q.Tests)
	q.Fresh = true
	return q
}

// FuncDiff is what an edit to one Go file changed, as the functions a
// coverage query can be asked about.
type FuncDiff struct {
	// Funcs is the functions of a production file whose text changed, were
	// added or were removed, as `Name` or `Recv.Name`, sorted.
	Funcs []string
	// TestFile says the file is a _test.go file, and Tests is then the runner
	// tests it declares now, sorted: all of them run, since an edit to one test
	// does not say which of the file's tests the next one reads.
	TestFile bool
	Tests    []string
	// Unmappable is why the edit cannot be reduced to functions: it changes a
	// declaration that is not a function (a var, const, type, import), an init,
	// TestMain or a test helper, a build directive, or a source that does not
	// parse. Empty when Funcs or Tests answers.
	Unmappable string
}

// DiffFuncs diffs the function spans of a file before and after an edit (old
// is nil for a new file). It reads each function by the span parseFuncSpans
// gives it, so its keys are the ones the store holds.
func DiffFuncs(oldSrc, newSrc []byte, testFile bool) FuncDiff {
	d := FuncDiff{TestFile: testFile}
	oldDecls, ok := diffDecls(oldSrc, testFile)
	if !ok {
		d.Unmappable = "the file before the edit does not parse"
		return d
	}
	newDecls, ok := diffDecls(newSrc, testFile)
	if !ok {
		d.Unmappable = "the file does not parse"
		return d
	}
	if oldDecls.pkg != "" && oldDecls.pkg != newDecls.pkg {
		d.Unmappable = "the package clause changed"
		return d
	}
	if !slices.Equal(oldDecls.directives, newDecls.directives) {
		d.Unmappable = "a build or go: directive changed"
		return d
	}
	if !slices.Equal(oldDecls.rest, newDecls.rest) {
		d.Unmappable = "a non-function declaration changed (var, const, type or import)"
		return d
	}
	changed := map[string]bool{}
	for key, text := range newDecls.funcs {
		if old, ok := oldDecls.funcs[key]; !ok || old != text {
			changed[key] = true
		}
	}
	for key := range oldDecls.funcs {
		if _, ok := newDecls.funcs[key]; !ok {
			changed[key] = true
		}
	}
	keys := make([]string, 0, len(changed))
	for key := range changed {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		name := key
		if i := strings.LastIndex(key, "."); i >= 0 {
			name = key[i+1:]
		}
		if name == "init" || name == "_" {
			d.Unmappable = "an init function changed"
			return d
		}
		if testFile && !newDecls.tests[key] && !oldDecls.tests[key] {
			d.Unmappable = fmt.Sprintf("the test helper %s changed", key)
			return d
		}
	}
	if len(keys) == 0 {
		d.Unmappable = "no function changed (a comment or layout edit)"
		return d
	}
	if testFile {
		for key := range newDecls.tests {
			d.Tests = append(d.Tests, key)
		}
		slices.Sort(d.Tests)
		if len(d.Tests) == 0 {
			d.Unmappable = "the test file declares no test"
		}
		return d
	}
	d.Funcs = keys
	return d
}

// declSet is a file's declarations as the diff compares them.
type declSet struct {
	pkg string
	// funcs is the text of each function by key, tests the keys that are
	// runner tests (a Test function with no receiver).
	funcs map[string]string
	tests map[string]bool
	// rest is the text of every other declaration, sorted; directives the
	// //go: and +build lines of the file, in order.
	rest       []string
	directives []string
}

// diffDecls reads the declarations of src; an empty source reads as a file
// with none.
func diffDecls(src []byte, testFile bool) (declSet, bool) {
	set := declSet{funcs: map[string]string{}, tests: map[string]bool{}}
	if len(src) == 0 {
		return set, true
	}
	// An autocrlf checkout differs from HEAD in its line endings alone.
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	fset, file, _ := parseFuncSpans(src)
	if file == nil {
		return set, false
	}
	set.pkg = file.Name.Name
	for line := range strings.Lines(string(src)) {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "//go:") || strings.HasPrefix(t, "// +build") {
			set.directives = append(set.directives, t)
		}
	}
	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			key := funcKey(d)
			if prev, dup := set.funcs[key]; dup {
				// init functions and blank functions repeat a key.
				set.funcs[key] = prev + "\n" + text(d)
			} else {
				set.funcs[key] = text(d)
			}
			if d.Recv == nil && runnerTestKind(d.Name.Name) == kindTest {
				set.tests[key] = true
			}
		case *ast.GenDecl:
			if d.Tok != token.IMPORT {
				set.rest = append(set.rest, text(d))
				continue
			}
			for _, spec := range d.Specs {
				imp, ok := spec.(*ast.ImportSpec)
				if !ok {
					continue
				}
				// A plain import of a test file only supplies names its functions
				// use, and those are diffed; a blank or dot import runs code or
				// changes names for the whole package.
				plain := imp.Name == nil || (imp.Name.Name != "_" && imp.Name.Name != ".")
				if plain && testFile {
					continue
				}
				set.rest = append(set.rest, "import "+text(imp))
			}
		}
	}
	slices.Sort(set.rest)
	return set, true
}
