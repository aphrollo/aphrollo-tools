package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// Move is one file the run relocates.
type Move struct {
	From, To string // repo-relative
	Pkg      string // package clause the moved Go file takes
	GoFile   bool   // a Go source of the package, not testdata or an embed
}

// Analysis is everything a run would do, computed before it touches the tree.
type Analysis struct {
	Generated map[string][]byte // repo-relative path -> content
	Stale     []string          // generated files on disk the run no longer writes
	Moves     []Move
	Reports   []string
}

// Analyze reassembles the unsplit package, maps every file through the
// manifest, and computes the moves and alias files for the given levels. A
// file the manifest does not map, or an embed or fuzz corpus separated from
// the file that owns it, refuses the whole run.
func Analyze(repo string, m *Manifest, levels map[int]bool) (*Analysis, error) {
	root := ""
	for _, p := range m.Packages {
		if p.Dir == m.Root {
			root = p.Name
		}
	}
	if root == "" {
		return nil, fmt.Errorf("manifest: no package lives in the root dir %s", m.Root)
	}
	srcs, err := listSources(repo, m)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, s := range srcs {
		keys = append(keys, s.Key)
	}
	if u := m.Unmapped(keys); len(u) > 0 {
		return nil, fmt.Errorf("refusing: %d file(s) the manifest does not map:\n  %s", len(u), strings.Join(u, "\n  "))
	}
	module, err := modulePath(repo)
	if err != nil {
		return nil, err
	}
	s := &splitter{m: m, root: root, levels: levels, module: module, needs: map[string]map[string]*need{}}
	a := &Analysis{Generated: map[string][]byte{}}
	for _, src := range srcs {
		p := m.Packages[src.Target]
		if s.eff(src) == src.Target && src.Target != root && src.Dir != p.Dir {
			a.Moves = append(a.Moves, Move{From: src.Path, To: p.Dir + "/" + src.Key, Pkg: p.Name, GoFile: src.isGo()})
		}
	}
	perGOOS := map[string]map[outKey]map[string]entry{}
	for _, goos := range []string{"linux", "windows"} {
		c, external, err := typecheck(repo, root, importPathOf(module, m.Root), srcs, goos)
		if err != nil {
			return nil, err
		}
		for _, e := range external {
			s.report("external test file %s (package *_test) is moved but its imports are not rewritten", e)
		}
		if goos == "linux" {
			errs, warns := ownership(c, m, srcs)
			if len(errs) > 0 {
				return nil, fmt.Errorf("refusing: the manifest separates files from what they own:\n  %s", strings.Join(errs, "\n  "))
			}
			for _, w := range warns {
				s.report("%s", w)
			}
		}
		s.relocations(c)
		s.needs = map[string]map[string]*need{}
		s.collect(c)
		if err := s.externalDemand(repo, c); err != nil {
			return nil, err
		}
		perGOOS[goos] = s.render(c)
	}
	a.Reports = append(s.reports, s.siteReports()...)
	gen, err := combine(perGOOS)
	if err != nil {
		return nil, err
	}
	a.Generated = gen
	a.Stale = staleGenerated(repo, m, a.Generated)
	return a, nil
}

// need is one package-level object a consumer package uses from another.
type need struct {
	obj     types.Object
	from    string
	prodUse bool
}

type splitter struct {
	m       *Manifest
	root    string
	levels  map[int]bool
	module  string
	needs   map[string]map[string]*need // consumer -> "from.name" -> need
	seams   map[types.Object]bool
	reports []string
	seen    map[string]bool
	sites   map[string]map[string]bool // finding -> use sites
}

// site records one use site of a finding; siteReports renders each finding
// once with every site it was seen at.
func (s *splitter) site(finding, where string) {
	if s.sites == nil {
		s.sites = map[string]map[string]bool{}
	}
	if s.sites[finding] == nil {
		s.sites[finding] = map[string]bool{}
	}
	s.sites[finding][where] = true
}

func (s *splitter) siteReports() []string {
	var out []string
	for finding, sites := range s.sites {
		list := make([]string, 0, len(sites))
		for w := range sites {
			list = append(list, w)
		}
		sort.Strings(list)
		out = append(out, fmt.Sprintf("%s: %d site(s): %s", finding, len(list), strings.Join(list, ", ")))
	}
	sort.Strings(out)
	return out
}

func (s *splitter) report(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if !s.seen[msg] {
		s.seen[msg] = true
		s.reports = append(s.reports, msg)
	}
}

// eff is the package a file belongs to after this run: its target when that
// target's level is being carved (or it already lives there), else the root.
func (s *splitter) eff(f srcFile) string {
	p := s.m.Packages[f.Target]
	if f.Target == s.root || p.Level == LevelPrep {
		return s.root
	}
	if s.levels[p.Level] || f.Dir == p.Dir {
		return f.Target
	}
	return s.root
}

func (s *splitter) level(pkg string) int { return s.m.Packages[pkg].Level }

// collect walks every identifier use and records, per consumer package, the
// package-level objects it needs from another; everything the split cannot
// alias is reported instead.
func (s *splitter) collect(c *checked) {
	s.seams = seamVars(c)
	scope := c.Pkg.Scope()
	idents := make([]*ast.Ident, 0, len(c.Info.Uses))
	for id := range c.Info.Uses {
		idents = append(idents, id)
	}
	sort.Slice(idents, func(i, j int) bool { return idents[i].Pos() < idents[j].Pos() })
	for _, id := range idents {
		obj := c.Info.Uses[id]
		if obj.Pkg() != c.Pkg {
			continue
		}
		user, ok1 := c.srcOf(id.Pos())
		decl, ok2 := c.srcOf(obj.Pos())
		if !ok1 || !ok2 {
			continue
		}
		a, b := s.eff(user), s.eff(decl)
		if a == b {
			continue
		}
		at := c.Fset.Position(id.Pos())
		where := fmt.Sprintf("%s:%d", user.Path, at.Line)
		if scope.Lookup(obj.Name()) != obj {
			if !obj.Exported() {
				dl := c.Fset.Position(obj.Pos()).Line
				kind := "field"
				if _, isFn := obj.(*types.Func); isFn {
					kind = "method"
				}
				s.site(fmt.Sprintf("export %s %s (declared %s:%d, package %s), read from package %s", kind, obj.Name(), decl.Path, dl, b, a), where)
			}
			continue
		}
		if decl.isTest() {
			s.site(fmt.Sprintf("test helper %s (declared in %s, package %s) used from package %s; a test helper cannot cross a package boundary, it belongs in tddtest", obj.Name(), decl.Path, b, a), where)
			continue
		}
		if s.level(b) >= s.level(a) {
			s.site(fmt.Sprintf("upward reference: package %s (L%d) uses %s declared in %s (package %s, L%d)", a, s.level(a), obj.Name(), decl.Path, b, s.level(b)), where)
			continue
		}
		s.addNeed(c, a, b, obj, !user.isTest())
	}
	s.methodsBesideTheirType(c)
}

func (s *splitter) addNeed(c *checked, consumer, from string, obj types.Object, prod bool) {
	if s.needs[consumer] == nil {
		s.needs[consumer] = map[string]*need{}
	}
	key := from + "." + obj.Name()
	n, ok := s.needs[consumer][key]
	if ok && (n.prodUse || !prod) {
		return
	}
	if !ok {
		n = &need{obj: obj, from: from}
		s.needs[consumer][key] = n
	}
	n.prodUse = n.prodUse || prod
	// A function's signature names types the consumer must also resolve.
	if fn, isFn := callable(obj); isFn {
		for _, tn := range namedIn(fn, c.Pkg) {
			decl, ok := c.srcOf(tn.Pos())
			if ok && s.eff(decl) != consumer && !decl.isTest() {
				s.addNeed(c, consumer, s.eff(decl), tn, prod)
			}
		}
	}
}

// methodsBesideTheirType reports a method declared in a file that lands in a
// different package from its receiver's type: Go refuses that outright.
func (s *splitter) methodsBesideTheirType(c *checked) {
	for _, f := range c.Files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil {
				continue
			}
			fn, _ := c.Info.Defs[fd.Name].(*types.Func)
			if fn == nil {
				continue
			}
			recv := fn.Signature().Recv().Type()
			if p, ok := recv.(*types.Pointer); ok {
				recv = p.Elem()
			}
			named, ok := recv.(*types.Named)
			if !ok {
				continue
			}
			mf, tf := c.ByFile[f], srcFile{}
			if t, ok := c.srcOf(named.Obj().Pos()); ok {
				tf = t
			}
			if s.eff(mf) != s.eff(tf) {
				s.report("method %s.%s in %s (package %s) is separated from its type in %s (package %s)", named.Obj().Name(), fn.Name(), mf.Path, s.eff(mf), tf.Path, s.eff(tf))
			}
		}
	}
}

// seamVars returns every package-level var any file reassigns, takes the
// address of, or mutates in place when it holds a value type.
func seamVars(c *checked) map[types.Object]bool {
	out := map[types.Object]bool{}
	scope := c.Pkg.Scope()
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
				if v, ok := c.Info.Uses[x].(*types.Var); ok && scope.Lookup(v.Name()) == v {
					out[v] = true
				}
			}
			return
		}
	}
	for _, f := range c.Files {
		ast.Inspect(f, func(n ast.Node) bool {
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
	}
	return out
}

// valueTyped reports whether writing through e changes the variable itself
// (a struct or array held by value) rather than memory it points at.
func valueTyped(c *checked, e ast.Expr) bool {
	tv, ok := c.Info.Types[e]
	if !ok {
		return false
	}
	switch tv.Type.Underlying().(type) {
	case *types.Struct, *types.Array:
		return true
	}
	return false
}

// callable returns the signature an object is called through: a func, or a
// func-typed var.
func callable(obj types.Object) (*types.Signature, bool) {
	switch o := obj.(type) {
	case *types.Func:
		return o.Signature(), true
	case *types.Var:
		sig, ok := o.Type().Underlying().(*types.Signature)
		return sig, ok
	}
	return nil, false
}

// namedIn returns the type names of pkg a signature mentions.
func namedIn(sig *types.Signature, pkg *types.Package) []*types.TypeName {
	var out []*types.TypeName
	seen := map[*types.TypeName]bool{}
	var walk func(t types.Type)
	walk = func(t types.Type) {
		switch x := t.(type) {
		case *types.Named:
			if tn := x.Obj(); tn.Pkg() == pkg && !seen[tn] {
				seen[tn] = true
				out = append(out, tn)
			}
		case *types.Alias:
			if tn := x.Obj(); tn.Pkg() == pkg && !seen[tn] {
				seen[tn] = true
				out = append(out, tn)
			}
		case *types.Pointer:
			walk(x.Elem())
		case *types.Slice:
			walk(x.Elem())
		case *types.Array:
			walk(x.Elem())
		case *types.Map:
			walk(x.Key())
			walk(x.Elem())
		case *types.Chan:
			walk(x.Elem())
		case *types.Signature:
			for i := 0; i < x.Params().Len(); i++ {
				walk(x.Params().At(i).Type())
			}
			for i := 0; i < x.Results().Len(); i++ {
				walk(x.Results().At(i).Type())
			}
		}
	}
	walk(sig)
	return out
}
