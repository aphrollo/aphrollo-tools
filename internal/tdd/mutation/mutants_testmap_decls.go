package mutation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The test map knows the tests a package had when it was built. What the
// package has NOW, and which of it this commit touched, is read from the test
// files themselves: a test the map never saw, or one the commit edited, is
// selected for every mutant because the map cannot say what it executes.

// declKind is what a top-level declaration of a test file is to the runner.
type declKind int

const (
	// kindHelper is any declaration the runner does not run: a helper, a
	// package variable, a type, a benchmark.
	kindHelper declKind = iota
	// kindTest is a Test, Fuzz or Example function.
	kindTest
	// kindMain is TestMain, which runs around every test of the package.
	kindMain
)

// testDecl is one top-level declaration of a package's test file, with the
// lines it covers. File is repo-relative and slash-separated.
type testDecl struct {
	Name       string
	File       string
	Start, End int
	Kind       declKind
}

// runnerTestKind classifies a function name the way the test runner does: a
// Test, Fuzz or Example prefix followed by nothing or by anything but a
// lowercase letter.
func runnerTestKind(name string) declKind {
	if name == "TestMain" {
		return kindMain
	}
	for _, prefix := range []string{"Test", "Fuzz", "Example"} {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		if rest == "" {
			return kindTest
		}
		if r, _ := utf8.DecodeRuneInString(rest); !unicode.IsLower(r) {
			return kindTest
		}
	}
	return kindHelper
}

// scanTestDecls reads the top-level declarations of every _test.go file in
// dir, keyed under rel, the directory's repo-relative path. A file that does
// not parse contributes nothing, and a directory that cannot be read has no
// declarations.
func scanTestDecls(dir, rel string) []testDecl {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var decls []testDecl
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			d := testDecl{
				File:  path.Join(filepath.ToSlash(rel), e.Name()),
				Start: fset.Position(decl.Pos()).Line,
				End:   fset.Position(decl.End()).Line,
			}
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				d.Name = decl.Name.Name
				if decl.Recv == nil {
					d.Kind = runnerTestKind(d.Name)
				}
			case *ast.GenDecl:
				if decl.Tok == token.IMPORT {
					continue
				}
			}
			decls = append(decls, d)
		}
	}
	return decls
}

// testNames is the tests the runner would run from decls, sorted and
// without repeats.
func testNames(decls []testDecl) []string {
	var names []string
	for _, d := range decls {
		if d.Kind == kindTest {
			names = append(names, d.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// testChanges is which tests the added lines touch. A test with an added line
// inside it is touched; a helper, variable or type of a test file with one
// touches every test of that file, since the tests cannot be told apart by
// what they use; TestMain with one means nothing can be told apart at all,
// and whole is true. Blank and comment lines sit in no declaration and touch
// nothing.
func testChanges(decls []testDecl, added map[string]map[int]bool) (touched []string, whole bool) {
	touchedSet := map[string]bool{}
	helperFiles := map[string]bool{}
	for _, d := range decls {
		if !anyLineIn(added[d.File], d.Start, d.End) {
			continue
		}
		switch d.Kind {
		case kindMain:
			whole = true
		case kindTest:
			touchedSet[d.Name] = true
		default:
			helperFiles[d.File] = true
		}
	}
	for _, d := range decls {
		if d.Kind == kindTest && helperFiles[d.File] {
			touchedSet[d.Name] = true
		}
	}
	for name := range touchedSet {
		touched = append(touched, name)
	}
	slices.Sort(touched)
	return touched, whole
}

// anyLineIn reports whether lines holds a line from start to end inclusive.
func anyLineIn(lines map[int]bool, start, end int) bool {
	for l := start; l <= end; l++ {
		if lines[l] {
			return true
		}
	}
	return false
}
