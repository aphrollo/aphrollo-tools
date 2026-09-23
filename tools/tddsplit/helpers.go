package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/types"
	"sort"
)

// helperBase names the generated test file that carries shared test helpers
// into each package whose tests call them across the split.
const helperBase = "tddtest_wrappers"

// carryHelper records that consumer's tests call fn, a func declared in a
// test file that lands in another package. The helper's declaration is
// copied into the consumer, so a moved test keeps calling it by the same
// name. Everything the copy reaches comes along too: another test helper is
// carried the same way, a lower-level object is aliased like any other use,
// and an object of a higher level is reported, because no alias can reach
// it.
func (s *splitter) carryHelper(c *checked, consumer string, fn *types.Func, where string) {
	if s.helpers[consumer] == nil {
		s.helpers[consumer] = map[*types.Func]bool{}
	}
	if s.helpers[consumer][fn] {
		return
	}
	fd := s.funcDecl(c, fn)
	if fd == nil || fd.Recv != nil {
		s.site(fmt.Sprintf("test helper %s used from package %s has no plain func declaration to carry", fn.Name(), consumer), where)
		return
	}
	s.helpers[consumer][fn] = true
	scope := c.Pkg.Scope()
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := c.Info.Uses[id]
		if obj == nil || obj.Pkg() != c.Pkg || scope.Lookup(obj.Name()) != obj {
			return true
		}
		decl, ok := c.srcOf(obj.Pos())
		if !ok {
			return true
		}
		from := s.eff(decl)
		if from == consumer {
			return true
		}
		at := where
		if hs, ok := c.srcOf(id.Pos()); ok {
			at = fmt.Sprintf("%s:%d", hs.Path, c.Fset.Position(id.Pos()).Line)
		}
		if helper, isFn := obj.(*types.Func); isFn && decl.isTest() {
			s.carryHelper(c, consumer, helper, at)
			return true
		}
		if decl.isTest() {
			s.site(fmt.Sprintf("test helper %s reaches %s (declared in %s, package %s), which is not a func and cannot be carried into package %s", fn.Name(), obj.Name(), decl.Path, from, consumer), at)
			return true
		}
		if s.level(from) >= s.level(consumer) {
			s.site(fmt.Sprintf("test helper %s, carried into package %s (L%d), uses %s declared in %s (package %s, L%d)", fn.Name(), consumer, s.level(consumer), obj.Name(), decl.Path, from, s.level(from)), at)
			return true
		}
		s.addNeed(c, consumer, from, obj, false)
		return true
	})
}

// funcDecl finds the declaration of fn among the checked files.
func (s *splitter) funcDecl(c *checked, fn *types.Func) *ast.FuncDecl {
	for _, f := range c.Files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && c.Info.Defs[fd.Name] == fn {
				return fd
			}
		}
	}
	return nil
}

// renderHelpers adds, per consumer, one entry per carried helper: its
// declaration printed without its doc comment, plus the imports its body
// names.
func (s *splitter) renderHelpers(c *checked, add func(outKey, entry)) {
	var consumers []string
	for cons := range s.helpers {
		consumers = append(consumers, cons)
	}
	sort.Strings(consumers)
	for _, cons := range consumers {
		key := outKey{Dir: s.m.Packages[cons].Dir, Pkg: cons, Base: helperBase, Test: true}
		for fn := range s.helpers[cons] {
			fd := s.funcDecl(c, fn)
			bare := *fd
			bare.Doc = nil
			var b bytes.Buffer
			if err := printer.Fprint(&b, c.Fset, &bare); err != nil {
				s.report("test helper %s: %v", fn.Name(), err)
				continue
			}
			imports := map[string]string{}
			ast.Inspect(fd, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if pn, ok := c.Info.Uses[id].(*types.PkgName); ok {
						imports[pn.Imported().Path()] = pn.Name()
					}
				}
				return true
			})
			add(key, entry{Order: orderFunc, Name: fn.Name(), Text: b.String(), Imports: imports})
		}
	}
}
