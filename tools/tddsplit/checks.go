package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ownership checks that the manifest keeps every embedded file with the Go
// file that embeds it and every fuzz corpus with the file declaring its fuzz
// target. Both are exact, so a mismatch is an error. Which test reads which
// testdata file is only a heuristic (a string literal naming it), so a
// mismatch there comes back as a warning.
func ownership(c *checked, m *Manifest, srcs []srcFile) (errs, warns []string) {
	fuzzOwner := map[string]srcFile{}
	for _, f := range c.Files {
		owner := c.ByFile[f]
		for _, cg := range f.Comments {
			for _, cm := range cg.List {
				rest, ok := strings.CutPrefix(cm.Text, "//go:embed ")
				if !ok {
					continue
				}
				for _, pat := range strings.Fields(rest) {
					for _, s := range srcs {
						if s.isGo() {
							continue
						}
						if hit, _ := path.Match(pat, s.Key); (hit || strings.HasPrefix(s.Key, pat+"/")) && s.Target != owner.Target {
							errs = append(errs, fmt.Sprintf("%s (package %s) embeds %s, mapped to %s", owner.Key, owner.Target, s.Key, s.Target))
						}
					}
				}
			}
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && strings.HasPrefix(fd.Name.Name, "Fuzz") {
				fuzzOwner[fd.Name.Name] = owner
			}
		}
	}
	readers := testdataReaders(c)
	for _, s := range srcs {
		rest, ok := strings.CutPrefix(s.Key, "testdata/")
		if !ok {
			continue
		}
		if corpus, ok := strings.CutPrefix(rest, "fuzz/"); ok {
			target, _, _ := strings.Cut(corpus, "/")
			owner, found := fuzzOwner[target]
			switch {
			case !found:
				warns = append(warns, fmt.Sprintf("fuzz corpus %s has no %s in any Go file", s.Key, target))
			case owner.Target != s.Target:
				errs = append(errs, fmt.Sprintf("fuzz corpus %s (package %s) is separated from %s in %s (package %s)", s.Key, s.Target, target, owner.Key, owner.Target))
			}
			continue
		}
		first, _, _ := strings.Cut(rest, "/")
		pkgs := map[string]bool{}
		var files []string
		for _, r := range readers[first] {
			pkgs[r.Target] = true
			files = append(files, r.Key)
		}
		if len(pkgs) == 0 {
			warns = append(warns, fmt.Sprintf("testdata %s: no Go file names %q in a string literal", s.Key, first))
			continue
		}
		if !pkgs[s.Target] || len(pkgs) > 1 {
			sort.Strings(files)
			warns = append(warns, fmt.Sprintf("testdata %s (package %s) is read by %s", s.Key, s.Target, strings.Join(files, ", ")))
		}
	}
	sort.Strings(errs)
	sort.Strings(warns)
	return errs, warns
}

// testdataReaders maps the first path element under testdata/ to the files
// whose string literals name it.
func testdataReaders(c *checked) map[string][]srcFile {
	out := map[string][]srcFile{}
	for _, f := range c.Files {
		owner := c.ByFile[f]
		seen := map[string]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			v = strings.TrimPrefix(v, "testdata/")
			first, _, _ := strings.Cut(v, "/")
			if first != "" && !seen[first] {
				seen[first] = true
				out[first] = append(out[first], owner)
			}
			return true
		})
	}
	return out
}

// relocations reports every manifest relocation a prep PR has not made yet.
func (s *splitter) relocations(c *checked) {
	scope := c.Pkg.Scope()
	here := map[string]bool{}
	for _, f := range c.ByFile {
		here[f.Key] = true
	}
	for _, r := range s.m.Relocations {
		if !here[r.From] && !here[r.To] {
			continue // the other platform's half of a GOOS pair
		}
		typ, method, isMethod := strings.Cut(r.Symbol, ".")
		obj := scope.Lookup(typ)
		if obj != nil && isMethod {
			obj, _, _ = types.LookupFieldOrMethod(obj.Type(), true, c.Pkg, method)
		}
		if obj == nil {
			s.report("relocation %s: no such symbol in the package", r.Symbol)
			continue
		}
		decl, ok := c.srcOf(obj.Pos())
		switch {
		case !ok:
			s.report("relocation %s: declaration not found in any source file", r.Symbol)
		case decl.Key == r.From:
			s.report("pending relocation: %s is still declared in %s (moves to %s)", r.Symbol, r.From, r.To)
		case decl.Key != r.To:
			s.report("relocation %s: declared in %s, which is neither %s nor %s", r.Symbol, decl.Key, r.From, r.To)
		}
	}
}

// extUse is one selector another package of the module applies to the root
// package's import.
type extUse struct {
	name     string
	where    string
	assigned bool
}

// externalDemand adds, to the root package, every symbol another package of
// the module selects from it whose declaration moves out of the root.
func (s *splitter) externalDemand(repo string, c *checked) error {
	uses, err := externalUses(repo, importPathOf(s.module, s.m.Root), s.m.Root, s.root)
	if err != nil {
		return err
	}
	scope := c.Pkg.Scope()
	for _, u := range uses {
		obj := scope.Lookup(u.name)
		if obj == nil {
			continue
		}
		decl, ok := c.srcOf(obj.Pos())
		if !ok || s.eff(decl) == s.root {
			continue
		}
		if u.assigned {
			s.seams[obj] = true
			s.report("%s assigns %s.%s, which moves to package %s: the assignment no longer reaches it", u.where, s.root, u.name, s.eff(decl))
		}
		s.addNeed(c, s.root, s.eff(decl), obj, true)
	}
	return nil
}

// externalUses parses every Go file of the module outside rootDir and returns
// each selector on an import of importPath.
func externalUses(repo, importPath, rootDir, defaultName string) ([]extUse, error) {
	var out []extUse
	fset := token.NewFileSet()
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repo, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel == rootDir || (rel != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		local := ""
		for _, im := range f.Imports {
			if strings.Trim(im.Path.Value, `"`) == importPath {
				local = defaultName
				if im.Name != nil {
					local = im.Name.Name
				}
			}
		}
		if local == "" {
			return nil
		}
		assigned := map[*ast.SelectorExpr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for _, l := range x.Lhs {
					if sel, ok := l.(*ast.SelectorExpr); ok {
						assigned[sel] = true
					}
				}
			case *ast.UnaryExpr:
				if sel, ok := x.X.(*ast.SelectorExpr); ok && x.Op == token.AND {
					assigned[sel] = true
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == local {
					out = append(out, extUse{name: x.Sel.Name, where: fmt.Sprintf("%s:%d", rel, fset.Position(x.Pos()).Line), assigned: assigned[x]})
				}
			}
			return true
		})
		return nil
	})
	return out, err
}
