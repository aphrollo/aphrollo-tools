package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"sort"
)

// helperBase names the generated test file that carries shared test helpers
// into each package whose tests call them across the split.
const helperBase = "tddtest_wrappers"

// helperDecl is the source of one carriable test helper: a plain func, or
// one name of a const spec with an explicit value.
type helperDecl struct {
	fn    *ast.FuncDecl
	spec  *ast.ValueSpec
	index int
}

// body is the part of the declaration whose references come along with it.
func (h helperDecl) body() ast.Node {
	if h.fn != nil {
		return h.fn.Body
	}
	return h.spec.Values[h.index]
}

// carryHelper records that consumer's tests use obj, a func or const
// declared in a test file that lands in another package. The helper's
// declaration is copied into the consumer, so a moved test keeps naming it.
// Everything the copy reaches comes along too: another test helper is
// carried the same way, a lower-level object is aliased like any other use,
// and an object of a higher level is reported, because no alias can reach
// it.
func (s *splitter) carryHelper(c *checked, consumer string, obj types.Object, where string) {
	if s.helpers[consumer] == nil {
		s.helpers[consumer] = map[types.Object]bool{}
	}
	if s.helpers[consumer][obj] {
		return
	}
	h, ok := s.helperDecl(c, obj)
	if !ok {
		s.site(fmt.Sprintf("test helper %s used from package %s has no plain func or valued const declaration to carry", obj.Name(), consumer), where)
		return
	}
	s.helpers[consumer][obj] = true
	scope := c.Pkg.Scope()
	ast.Inspect(h.body(), func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		used := c.Info.Uses[id]
		if used == nil || used.Pkg() != c.Pkg || scope.Lookup(used.Name()) != used {
			return true
		}
		decl, ok := c.srcOf(used.Pos())
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
		if decl.isTest() && carriable(used) {
			s.carryHelper(c, consumer, used, at)
			return true
		}
		if decl.isTest() {
			s.site(fmt.Sprintf("test helper %s reaches %s (declared in %s, package %s), which is neither a func nor a const and cannot be carried into package %s", obj.Name(), used.Name(), decl.Path, from, consumer), at)
			return true
		}
		if s.level(from) >= s.level(consumer) {
			s.site(fmt.Sprintf("test helper %s, carried into package %s (L%d), uses %s declared in %s (package %s, L%d)", obj.Name(), consumer, s.level(consumer), used.Name(), decl.Path, from, s.level(from)), at)
			return true
		}
		s.addNeed(c, consumer, from, used, false)
		return true
	})
}

// carriable reports whether a test-file object is a kind carryHelper copies.
func carriable(obj types.Object) bool {
	switch obj.(type) {
	case *types.Func, *types.Const:
		return true
	}
	return false
}

// helperDecl finds the declaration of a carriable helper among the checked
// files: a func without a receiver, or a const name with its own value.
func (s *splitter) helperDecl(c *checked, obj types.Object) (helperDecl, bool) {
	for _, f := range c.Files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if c.Info.Defs[d.Name] == obj {
					return helperDecl{fn: d}, d.Recv == nil
				}
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, sp := range d.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if c.Info.Defs[name] == obj {
							return helperDecl{spec: vs, index: i}, i < len(vs.Values)
						}
					}
				}
			}
		}
	}
	return helperDecl{}, false
}

// renderHelpers adds, per consumer, one entry per carried helper: a func's
// declaration printed without its doc comment, or `const name [type] =
// value`, plus the imports it names.
func (s *splitter) renderHelpers(c *checked, add func(outKey, entry)) {
	var consumers []string
	for cons := range s.helpers {
		consumers = append(consumers, cons)
	}
	sort.Strings(consumers)
	for _, cons := range consumers {
		key := outKey{Dir: s.m.Packages[cons].Dir, Pkg: cons, Base: helperBase, Test: true}
		for obj := range s.helpers[cons] {
			h, _ := s.helperDecl(c, obj)
			var b bytes.Buffer
			var err error
			var node ast.Node
			order := orderFunc
			if h.fn != nil {
				bare := *h.fn
				bare.Doc = nil
				node = &bare
				err = printer.Fprint(&b, c.Fset, &bare)
			} else {
				node = h.spec
				order = orderConst
				b.WriteString("const " + obj.Name() + " ")
				if h.spec.Type != nil {
					err = printer.Fprint(&b, c.Fset, h.spec.Type)
					b.WriteString(" ")
				}
				b.WriteString("= ")
				if err == nil {
					err = printer.Fprint(&b, c.Fset, h.spec.Values[h.index])
				}
			}
			if err != nil {
				s.report("test helper %s: %v", obj.Name(), err)
				continue
			}
			imports := map[string]string{}
			ast.Inspect(node, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if pn, ok := c.Info.Uses[id].(*types.PkgName); ok {
						imports[pn.Imported().Path()] = pn.Name()
					}
				}
				return true
			})
			add(key, entry{Order: order, Name: obj.Name(), Text: b.String(), Imports: imports})
		}
	}
}
