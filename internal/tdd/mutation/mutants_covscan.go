package mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// What the incremental coverage map reads from a package's own source: its
// functions with a hash of each, the hash of everything else a file declares,
// the tests it has, and a static picture of who mentions whom. The hashes say
// which functions a commit changed; the picture picks the tests worth
// measuring first when no earlier map exists.

// pkgFunc is one function declaration (or, with Var set, one package-level
// variable or constant) of a package.
type pkgFunc struct {
	// Key is `Name` or `Recv.Name`, unique in the package, and `var:Name` for
	// a variable or constant.
	Key  string
	File string
	// Names is what the declaration is called, which is what other code
	// mentions: one name for a function or method, every name of a var spec.
	Names      []string
	Test       bool
	Var        bool
	Start, End int
	Hash       string
	// Refs is every identifier and selector name the body mentions.
	Refs []string
	// Global marks TestMain and init: they run around or before every test.
	Global bool
	// Reexec marks a function that starts the test binary again: it names
	// os.Args or os.Executable. What a child process executes is in no profile
	// of the parent.
	Reexec bool
}

// pkgScan is the source of one package directory.
type pkgScan struct {
	// Funcs holds the function and method declarations of every .go file, by
	// Key. Vars holds package-level variable and constant specs.
	Funcs map[string]pkgFunc
	Vars  []pkgFunc
	// Rest is the hash of the declarations of every file that are neither
	// functions nor imports, per file and then together, without the file
	// names, so a rename changes nothing. A change in one can alter what every
	// test does.
	Rest string
	// TestKey maps each runner test name (a Test function of a _test.go file)
	// to its function's Key.
	TestKey map[string]string
}

// testNameList is the runner test names of the scan, sorted.
func (s pkgScan) testNameList() []string {
	names := make([]string, 0, len(s.TestKey))
	for name := range s.TestKey {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// scanPackage reads every .go file directly in dir that the build, under the
// given tags, includes. A file that does not
// parse is an error: nothing can be said of a package that does not build.
func scanPackage(dir string, tags []string) (pkgScan, error) {
	ctxt := build.Default
	ctxt.BuildTags = slices.Clone(tags)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return pkgScan{}, err
	}
	scan := pkgScan{Funcs: map[string]pkgFunc{}, TestKey: map[string]string{}}
	var rests []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		// A file the build constraints leave out is not in the test binary, and
		// its twin for another platform must not shadow the one that is.
		if ok, err := ctxt.MatchFile(dir, name); err == nil && !ok {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return pkgScan{}, err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			return pkgScan{}, err
		}
		isTest := strings.HasSuffix(name, "_test.go")
		var rest strings.Builder
		seen := map[string]int{}
		text := func(n ast.Node) string {
			return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				base := funcKey(d)
				key := base
				if d.Name.Name == "init" || d.Name.Name == "_" {
					// More than one may stand in a package, so these are told apart
					// by where they are; every other key is the declared name, which
					// is what lets a function that moves to another file keep its
					// entries.
					key = fmt.Sprintf("%s@%s#%d", base, name, seen[base])
				}
				seen[base]++
				f := pkgFunc{
					Key: key, File: name, Names: []string{d.Name.Name}, Test: isTest,
					Start: fset.Position(d.Pos()).Line, End: fset.Position(d.End()).Line,
					Hash: hashText(text(d)), Refs: mentionedNames(d.Body), Reexec: namesOwnBinary(d.Body),
					Global: d.Name.Name == "init" || (isTest && d.Name.Name == "TestMain"),
				}
				scan.Funcs[key] = f
				if isTest && d.Recv == nil && runnerTestKind(d.Name.Name) == kindTest {
					scan.TestKey[d.Name.Name] = key
				}
			case *ast.GenDecl:
				if d.Tok == token.IMPORT {
					continue
				}
				rest.WriteString(text(d))
				rest.WriteByte('\n')
				if d.Tok != token.VAR && d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					var names []string
					for _, id := range vs.Names {
						names = append(names, id.Name)
					}
					scan.Vars = append(scan.Vars, pkgFunc{
						Key: "var:" + names[0], File: name, Names: names, Test: isTest, Var: true,
						Start: fset.Position(vs.Pos()).Line, End: fset.Position(vs.End()).Line,
						Refs: mentionedNames(vs), Hash: hashText(text(vs)),
					})
				}
			}
		}
		if rest.Len() > 0 {
			rests = append(rests, hashText(rest.String()))
		}
	}
	slices.Sort(rests)
	scan.Rest = hashText(strings.Join(rests, ","))
	return scan, nil
}

// namesOwnBinary reports whether n mentions os.Args or os.Executable, which is
// how a test finds its own binary to start it again.
func namesOwnBinary(n ast.Node) bool {
	found := false
	if n == nil {
		return false
	}
	ast.Inspect(n, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && (sel.Sel.Name == "Args" || sel.Sel.Name == "Executable") {
				found = true
			}
		}
		return !found
	})
	return found
}

// reexecTests is the runner tests that start the test binary again, in the
// function itself or in any helper or variable it reaches by name, sorted.
func (s pkgScan) reexecTests() []string {
	var seed []string
	for key, f := range s.Funcs {
		if f.Reexec {
			seed = append(seed, key)
		}
	}
	if len(seed) == 0 {
		return nil
	}
	closure := s.callersClosure(seed)
	var tests []string
	for name, key := range s.TestKey {
		if closure[key] {
			tests = append(tests, name)
		}
	}
	slices.Sort(tests)
	return tests
}

// hashText is the hex sha256 of s.
func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// mentionedNames is every identifier and selected name under n, sorted and
// without repeats. It over-approximates what n calls or uses: a method is
// mentioned by its name whatever its receiver, which is what makes a call
// through an interface or a method value visible.
func mentionedNames(n ast.Node) []string {
	if n == nil {
		return nil
	}
	set := map[string]bool{}
	ast.Inspect(n, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			set[x.Name] = true
		case *ast.SelectorExpr:
			set[x.Sel.Name] = true
		}
		return true
	})
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mentioners is the keys of every function and variable declaration of the
// scan that mentions any of names, directly.
func (s pkgScan) mentioners(names map[string]bool) []string {
	var keys []string
	add := func(f pkgFunc) {
		for _, r := range f.Refs {
			if names[r] {
				keys = append(keys, f.Key)
				return
			}
		}
	}
	for _, f := range s.Funcs {
		add(f)
	}
	for _, v := range s.Vars {
		add(v)
	}
	return keys
}

// callersClosure is seed and everything that reaches it by mention, through
// any number of functions, helpers and package variables: the declarations
// whose behavior a change to a seed function can change. It is static and
// by name. It misses a function reached only by reflection, by a func value
// another package hands back, or by a name the scan cannot see (generated
// code, cgo); that is why a map built from it alone is partial, and a mutant
// the tests it selects miss is NOT MEASURED, never a survivor.
func (s pkgScan) callersClosure(seed []string) map[string]bool {
	in := map[string]bool{}
	names := map[string]bool{}
	byKey := func(key string) (pkgFunc, bool) {
		if f, ok := s.Funcs[key]; ok {
			return f, true
		}
		for _, v := range s.Vars {
			if v.Key == key {
				return v, true
			}
		}
		return pkgFunc{}, false
	}
	queue := slices.Clone(seed)
	// walk-terminates: each key is expanded once (in[] guards), the keys are finite
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if in[key] {
			continue
		}
		in[key] = true
		f, ok := byKey(key)
		if !ok {
			continue
		}
		fresh := map[string]bool{}
		for _, n := range f.Names {
			if !names[n] {
				names[n] = true
				fresh[n] = true
			}
		}
		if len(fresh) > 0 {
			queue = append(queue, s.mentioners(fresh)...)
		}
	}
	return in
}

// candidateTests is the runner tests whose function is in the callers closure
// of the named functions, sorted: the tests that can reach them.
func (s pkgScan) candidateTests(funcs []string) []string {
	closure := s.callersClosure(funcs)
	var tests []string
	for name, key := range s.TestKey {
		if closure[key] {
			tests = append(tests, name)
		}
	}
	slices.Sort(tests)
	return tests
}

// funcAt is the key of the non-test function of file (a base name) whose lines
// hold line, "" when none does.
func (s pkgScan) funcAt(file string, line int) string {
	if f, ok := s.declAt(file, line); ok && !f.Var {
		return f.Key
	}
	return ""
}

// declAt is the non-test function or package-level variable of file (a base
// name) whose lines hold line. A function literal in a variable's value is a
// block of the variable.
func (s pkgScan) declAt(file string, line int) (pkgFunc, bool) {
	for _, f := range s.Funcs {
		if f.File == file && !f.Test && f.Start <= line && line <= f.End {
			return f, true
		}
	}
	for _, v := range s.Vars {
		if v.File == file && !v.Test && v.Start <= line && line <= v.End {
			return v, true
		}
	}
	return pkgFunc{}, false
}

// decl is the function or variable declaration with the key.
func (s pkgScan) decl(key string) (pkgFunc, bool) {
	if f, ok := s.Funcs[key]; ok {
		return f, true
	}
	for _, v := range s.Vars {
		if v.Key == key {
			return v, true
		}
	}
	return pkgFunc{}, false
}
