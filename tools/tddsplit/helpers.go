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

// helperDecl is the source of one carriable test helper: a plain func, one
// name of a const or var spec with an explicit value, or a type spec.
type helperDecl struct {
	fn    *ast.FuncDecl
	ts    *ast.TypeSpec
	tok   token.Token // token.CONST or token.VAR for a spec
	spec  *ast.ValueSpec
	index int
}

// node is the whole declaration a copy reproduces: a func's signature and
// body, or a spec's type and value.
func (h helperDecl) nodes() []ast.Node {
	if h.fn != nil {
		return []ast.Node{h.fn.Type, h.fn.Body}
	}
	if h.ts != nil {
		return []ast.Node{h.ts.Type}
	}
	out := []ast.Node{h.spec.Values[h.index]}
	if h.spec.Type != nil {
		out = append(out, h.spec.Type)
	}
	return out
}

// carryHelper copies obj, a func, const or var declared in a test file that
// lands in another package, into consumer, so a moved test keeps naming it,
// and reports whether it did. Everything the copy reaches comes along: another
// test helper is carried the same way, and a lower-level object is aliased
// like any other use. A helper is refused, reported and never emitted when
// its copy could not compile or would not be the same thing: it names
// something of the consumer's own level or above (no alias reaches upward),
// reads an unexported member of a type landing elsewhere, is a var some test
// writes or one holding a lock, or relies on another helper refused for any
// of these.
func (s *splitter) carryHelper(c *checked, consumer string, obj types.Object, where string) bool {
	if s.helpers[consumer] == nil {
		s.helpers[consumer] = map[types.Object]bool{}
	}
	if s.helpers[consumer][obj] || s.carrying[obj] {
		return true
	}
	h, ok := s.helperDecl(c, obj)
	if !ok {
		s.site(fmt.Sprintf("test helper %s used from package %s has no plain func or valued const or var declaration to carry", obj.Name(), consumer), where)
		return false
	}
	if h.ts != nil && !h.ts.Assign.IsValid() {
		s.site(fmt.Sprintf("test type %s is a defined type, not an alias: a copy in package %s would be a second, distinct type; alias it to a tddtest type or move its users together", obj.Name(), consumer), where)
		return false
	}
	if _, isVar := obj.(*types.Var); isVar && (s.seams[obj] || holdsLock(obj.Type())) {
		s.site(fmt.Sprintf("test var %s is written by a test or holds a lock, so it is state, not a shared value: a copy in package %s would drift from the original; move its users together or hand it through tddtest", obj.Name(), consumer), where)
		return false
	}
	if s.readsUnexportedAcross(c, consumer, obj, h) {
		return false
	}
	type use struct {
		obj  types.Object
		from string
		at   string
		test bool
	}
	var uses []use
	refused := false
	scope := c.Pkg.Scope()
	for _, n := range h.nodes() {
		written := writtenIdents(c, n)
		ast.Inspect(n, func(n ast.Node) bool {
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
			if _, isVar := used.(*types.Var); isVar && written[id] {
				s.site(fmt.Sprintf("test helper %s is not carried into package %s: it writes %s (declared in %s, package %s), and across the split the write would reach only an alias; give package %s a Set...ForTest setter and call it", obj.Name(), consumer, used.Name(), decl.Path, from, from), at)
				refused = true
				return true
			}
			switch {
			case decl.isTest() && carriable(used):
				uses = append(uses, use{obj: used, from: from, at: at, test: true})
			case decl.isTest():
				s.site(fmt.Sprintf("test helper %s is not carried into package %s: it reaches %s (declared in %s, package %s), which is not a func, const, var or type", obj.Name(), consumer, used.Name(), decl.Path, from), at)
				refused = true
			case s.level(from) >= s.level(consumer):
				s.site(fmt.Sprintf("test helper %s is not carried into package %s (L%d): it names %s, declared in %s (package %s, L%d), and no alias reaches upward", obj.Name(), consumer, s.level(consumer), used.Name(), decl.Path, from, s.level(from)), at)
				refused = true
			default:
				uses = append(uses, use{obj: used, from: from, at: at})
			}
			return true
		})
	}
	if refused {
		return false
	}
	if s.carrying == nil {
		s.carrying = map[types.Object]bool{}
	}
	s.carrying[obj] = true
	defer delete(s.carrying, obj)
	for _, u := range uses {
		if u.test && !s.carryHelper(c, consumer, u.obj, u.at) {
			s.site(fmt.Sprintf("test helper %s is not carried into package %s: it relies on helper %s, which is not carried either", obj.Name(), consumer, u.obj.Name()), u.at)
			return false
		}
	}
	for _, u := range uses {
		if !u.test {
			s.addNeed(c, consumer, u.from, u.obj, false)
		}
	}
	s.helpers[consumer][obj] = true
	return true
}

// readsUnexportedAcross reports, one finding per site, every unexported
// field or method the helper's body reads on a type that lands in another
// package than consumer. A copy of such a helper would not compile there,
// so a helper with any is never carried.
func (s *splitter) readsUnexportedAcross(c *checked, consumer string, obj types.Object, h helperDecl) bool {
	found := false
	scope := c.Pkg.Scope()
	for _, root := range h.nodes() {
		ast.Inspect(root, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			used := c.Info.Uses[id]
			if used == nil || used.Pkg() != c.Pkg || used.Exported() || scope.Lookup(used.Name()) == used {
				return true
			}
			if _, local := used.(*types.PkgName); local {
				return true
			}
			decl, ok := c.srcOf(used.Pos())
			if !ok || decl.isTest() || s.eff(decl) == consumer || used.Parent() != nil {
				return true
			}
			kind := "field"
			if _, isFn := used.(*types.Func); isFn {
				kind = "method"
			}
			at := fmt.Sprintf("%s:%d", c.ByName[c.Fset.Position(id.Pos()).Filename].Path, c.Fset.Position(id.Pos()).Line)
			s.site(fmt.Sprintf("test helper %s is not carried into package %s: it reads unexported %s %s (declared in %s, package %s); export it", obj.Name(), consumer, kind, used.Name(), decl.Path, s.eff(decl)), at)
			found = true
			return true
		})
	}
	return found
}

// carriable reports whether a test-file object is a kind carryHelper copies:
// a func, a const, or a package-level var (a written one is refused inside
// carryHelper, with the reason).
func carriable(obj types.Object) bool {
	switch obj.(type) {
	case *types.Func, *types.Const, *types.Var, *types.TypeName:
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
				if d.Tok == token.TYPE {
					for _, sp := range d.Specs {
						if ts := sp.(*ast.TypeSpec); c.Info.Defs[ts.Name] == obj {
							return helperDecl{ts: ts}, ts.TypeParams == nil
						}
					}
					continue
				}
				if d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, sp := range d.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if c.Info.Defs[name] == obj {
							return helperDecl{tok: d.Tok, spec: vs, index: i}, i < len(vs.Values)
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
			if h.ts != nil {
				node = h.ts
				order = orderType
				b.WriteString("type " + obj.Name() + " = ")
				err = printer.Fprint(&b, c.Fset, h.ts.Type)
			} else if h.fn != nil {
				bare := *h.fn
				bare.Doc = nil
				node = &bare
				err = printer.Fprint(&b, c.Fset, &bare)
			} else {
				node = h.spec
				order = orderConst
				if h.tok == token.VAR {
					order = orderVar
				}
				b.WriteString(h.tok.String() + " " + obj.Name() + " ")
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

// writtenIdents returns the identifiers in n that name a variable being
// written: assigned, incremented, ranged into, or having its address taken,
// following writes through fields and elements of values held by value, the
// way seamVars judges a package var.
func writtenIdents(c *checked, n ast.Node) map[*ast.Ident]bool {
	out := map[*ast.Ident]bool{}
	mark := func(e ast.Expr) {
		for {
			switch x := e.(type) {
			case *ast.ParenExpr:
				e = x.X
				continue
			case *ast.SelectorExpr:
				if !valueTyped(c, x.X) {
					return
				}
				e = x.X
				continue
			case *ast.IndexExpr:
				if !valueTyped(c, x.X) {
					return
				}
				e = x.X
				continue
			case *ast.Ident:
				out[x] = true
			}
			return
		}
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if x.Tok != token.DEFINE {
				for _, l := range x.Lhs {
					mark(l)
				}
			}
		case *ast.IncDecStmt:
			mark(x.X)
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				mark(x.X)
			}
		case *ast.RangeStmt:
			if x.Tok == token.ASSIGN {
				if x.Key != nil {
					mark(x.Key)
				}
				if x.Value != nil {
					mark(x.Value)
				}
			}
		}
		return true
	})
	return out
}
