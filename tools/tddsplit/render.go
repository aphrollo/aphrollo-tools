package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// outKey names one generated file before its GOOS split.
type outKey struct {
	Dir  string // repo-relative package dir
	Pkg  string // package clause
	Base string // "export", "deps_<pkg>" or "api_<pkg>"
	Test bool
}

// entry is one generated declaration line and the imports it needs.
type entry struct {
	Order   int // const, type, var, func
	Name    string
	Text    string
	Imports map[string]string // import path -> local name
}

const (
	orderConst = iota
	orderType
	orderVar
	orderFunc
)

// render turns the collected needs into alias entries: in each consumer, a
// declaration under the original name pointing across the boundary; in each
// declaring package, an exported name for every unexported object used
// outside it.
func (s *splitter) render(c *checked) map[outKey]map[string]entry {
	out := map[outKey]map[string]entry{}
	add := func(k outKey, e entry) {
		if out[k] == nil {
			out[k] = map[string]entry{}
		}
		out[k][e.Text] = e
	}
	scope := c.Pkg.Scope()
	var consumers []string
	for cons := range s.needs {
		consumers = append(consumers, cons)
	}
	sort.Strings(consumers)
	for _, cons := range consumers {
		keys := make([]string, 0, len(s.needs[cons]))
		for k := range s.needs[cons] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n := s.needs[cons][k]
			name := n.obj.Name()
			exp := exportName(name)
			if exp != name && scope.Lookup(exp) != nil {
				s.report("export name %s is taken in package %s, so %s is exported as X%s", exp, n.from, name, name)
				exp = "X" + name
			}
			from := s.m.Packages[n.from]
			base := "deps_" + n.from
			if cons == s.root {
				base = "api_" + n.from
			}
			consKey := outKey{Dir: s.m.Packages[cons].Dir, Pkg: cons, Base: base, Test: !n.prodUse}
			expKey := outKey{Dir: from.Dir, Pkg: n.from, Base: "export"}
			alias := importName(n.from, scope)
			fromPath := importPathOf(s.module, from.Dir)
			consImports := map[string]string{fromPath: alias}
			expImports := map[string]string{}
			consQual := qualifier(c.Pkg, scope, consImports)
			expQual := qualifier(c.Pkg, scope, expImports)
			target := alias + "." + exp
			var consE, expE entry
			switch o := n.obj.(type) {
			case *types.Const:
				consE = entry{Order: orderConst, Text: fmt.Sprintf("const %s = %s", name, target)}
				expE = entry{Order: orderConst, Text: fmt.Sprintf("const %s = %s", exp, name)}
			case *types.TypeName:
				if named, ok := o.Type().(*types.Named); ok && named.TypeParams().Len() > 0 {
					s.report("generic type %s (package %s) needs a hand-written alias", name, n.from)
					continue
				}
				consE = entry{Order: orderType, Text: fmt.Sprintf("type %s = %s", name, target)}
				expE = entry{Order: orderType, Text: fmt.Sprintf("type %s = %s", exp, name)}
			case *types.Func:
				if o.Signature().TypeParams().Len() > 0 {
					s.report("generic func %s (package %s) needs a hand-written alias", name, n.from)
					continue
				}
				consE = entry{Order: orderFunc, Text: callThrough(name, target, o.Signature(), consQual)}
				expE = entry{Order: orderFunc, Text: callThrough(exp, name, o.Signature(), expQual)}
			case *types.Var:
				sig, isFn := o.Type().Underlying().(*types.Signature)
				switch {
				case s.seams[o] && isFn:
					consE = entry{Order: orderFunc, Text: callThrough(name, target, sig, consQual)}
					expE = entry{Order: orderFunc, Text: callThrough(exp, name, sig, expQual)}
				case s.seams[o]:
					s.report("seam var %s (package %s) is reassigned and not a func: it needs a hand-written accessor", name, n.from)
					continue
				default:
					consE = entry{Order: orderVar, Text: fmt.Sprintf("var %s = %s", name, target)}
					expE = entry{Order: orderVar, Text: fmt.Sprintf("var %s = %s", exp, name)}
				}
			default:
				continue
			}
			consE.Name, consE.Imports = name, consImports
			add(consKey, consE)
			if exp != name {
				expE.Name, expE.Imports = exp, expImports
				add(expKey, expE)
			}
		}
	}
	return out
}

// callThrough renders `func name(p0 T0, p1 ...T1) R { return target(p0, p1...) }`.
func callThrough(name, target string, sig *types.Signature, q types.Qualifier) string {
	var params, args []string
	for i := 0; i < sig.Params().Len(); i++ {
		t := sig.Params().At(i).Type()
		p := fmt.Sprintf("p%d", i)
		if sig.Variadic() && i == sig.Params().Len()-1 {
			params = append(params, p+" ..."+types.TypeString(t.(*types.Slice).Elem(), q))
			args = append(args, p+"...")
			continue
		}
		params = append(params, p+" "+types.TypeString(t, q))
		args = append(args, p)
	}
	var results []string
	for i := 0; i < sig.Results().Len(); i++ {
		results = append(results, types.TypeString(sig.Results().At(i).Type(), q))
	}
	res := strings.Join(results, ", ")
	if len(results) > 1 {
		res = "(" + res + ")"
	}
	if res != "" {
		res = " " + res
	}
	call := target + "(" + strings.Join(args, ", ") + ")"
	if len(results) > 0 {
		call = "return " + call
	}
	return fmt.Sprintf("func %s(%s)%s { %s }", name, strings.Join(params, ", "), res, call)
}

// qualifier prints the unsplit package's own types bare (every package of the
// split aliases them under their original names) and records every other
// package it prints as an import.
func qualifier(self *types.Package, scope *types.Scope, imports map[string]string) types.Qualifier {
	return func(p *types.Package) string {
		if p == self {
			return ""
		}
		name := importName(p.Name(), scope)
		imports[p.Path()] = name
		return name
	}
}

// importName is the local name a generated file imports a package under: its
// own name, unless the unsplit package already declares that identifier.
func importName(pkg string, scope *types.Scope) string {
	if scope.Lookup(pkg) != nil {
		return pkg + "pkg"
	}
	return pkg
}

func exportName(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(r)) + name[size:]
}

// combine splits every generated file by GOOS: declarations both platforms
// share go in the plain file, the rest in a `_windows` or `_nowindows`
// sibling, and renders each one gofmt-clean.
func combine(perGOOS map[string]map[outKey]map[string]entry) (map[string][]byte, error) {
	keys := map[outKey]bool{}
	for _, m := range perGOOS {
		for k := range m {
			keys[k] = true
		}
	}
	out := map[string][]byte{}
	for k := range keys {
		lin, win := perGOOS["linux"][k], perGOOS["windows"][k]
		var common, linOnly, winOnly []entry
		for text, e := range lin {
			if _, ok := win[text]; ok {
				common = append(common, e)
			} else {
				linOnly = append(linOnly, e)
			}
		}
		for text, e := range win {
			if _, ok := lin[text]; !ok {
				winOnly = append(winOnly, e)
			}
		}
		for _, v := range []struct {
			suffix, constraint string
			entries            []entry
		}{{"", "", common}, {"_nowindows", "!windows", linOnly}, {"_windows", "", winOnly}} {
			if len(v.entries) == 0 {
				continue
			}
			name := k.Base + v.suffix
			if k.Test {
				name += "_test"
			}
			body, err := renderFile(k.Pkg, v.constraint, v.entries)
			if err != nil {
				return nil, fmt.Errorf("%s/%s.go: %w", k.Dir, name, err)
			}
			out[k.Dir+"/"+name+".go"] = body
		}
	}
	return out, nil
}

func renderFile(pkg, constraint string, entries []entry) ([]byte, error) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Order != entries[j].Order {
			return entries[i].Order < entries[j].Order
		}
		return entries[i].Name < entries[j].Name
	})
	imports := map[string]string{}
	for _, e := range entries {
		for p, n := range e.Imports {
			imports[p] = n
		}
	}
	var b bytes.Buffer
	b.WriteString(genHeader + "\n\n")
	if constraint != "" {
		fmt.Fprintf(&b, "//go:build %s\n\n", constraint)
	}
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	if len(imports) > 0 {
		paths := make([]string, 0, len(imports))
		for p := range imports {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		b.WriteString("import (\n")
		for _, p := range paths {
			fmt.Fprintf(&b, "\t%s %q\n", imports[p], p)
		}
		b.WriteString(")\n\n")
	}
	for _, e := range entries {
		b.WriteString(e.Text + "\n\n")
	}
	return format.Source(b.Bytes())
}

// staleGenerated lists generated files under the root the run does not
// write again.
func staleGenerated(repo string, m *Manifest, gen map[string][]byte) []string {
	var out []string
	rootAbs := filepath.Join(repo, filepath.FromSlash(m.Root))
	_ = filepath.WalkDir(rootAbs, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !generated(p) {
			return nil
		}
		rel, _ := filepath.Rel(repo, p)
		rel = filepath.ToSlash(rel)
		if _, ok := gen[rel]; !ok {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
