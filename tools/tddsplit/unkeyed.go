package main

import (
	"go/ast"
	"go/types"
)

// unkeyedLiterals reports every composite literal with positional fields
// whose struct type is declared in a file that lands in another package than
// the literal. After the split the type is an alias of a struct from another
// package, and go vet's composites check refuses an unkeyed literal of one.
// An elided element type (`[]T{{...}}`) counts the same as a written one.
func (s *splitter) unkeyedLiterals(c *checked) {
	scope := c.Pkg.Scope()
	for _, f := range c.Files {
		user := c.ByFile[f]
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || len(lit.Elts) == 0 {
				return true
			}
			if _, keyed := lit.Elts[0].(*ast.KeyValueExpr); keyed {
				return true
			}
			tv, ok := c.Info.Types[lit]
			if !ok {
				return true
			}
			t := tv.Type
			if p, isPtr := t.(*types.Pointer); isPtr {
				t = p.Elem()
			}
			if _, isStruct := t.Underlying().(*types.Struct); !isStruct {
				return true
			}
			var obj *types.TypeName
			switch x := types.Unalias(t).(type) {
			case *types.Named:
				obj = x.Obj()
			default:
				return true
			}
			if obj.Pkg() != c.Pkg || scope.Lookup(obj.Name()) != obj {
				return true
			}
			decl, ok := c.srcOf(obj.Pos())
			if !ok {
				return true
			}
			a, b := s.eff(user), s.eff(decl)
			if a == b {
				return true
			}
			at := c.Fset.Position(lit.Pos())
			s.report("unkeyed composite literal of %s (declared in %s, package %s) in package %s: %s:%d:%d", obj.Name(), decl.Path, b, a, user.Path, at.Line, at.Column)
			return true
		})
	}
}
