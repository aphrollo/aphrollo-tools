package lawgate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
)

// A dependency-graph law answers for the module, not for a file, and asking
// costs a `go list` over every package: seconds on a real repo, paid by every
// Edit. Most edits cannot move the answer, so the pre-edit judge asks only
// when this one might have (graphMayChange) and, when it does, judges the
// graph the edit WOULD leave — the proposed sources handed to `go list` as an
// overlay — rather than the disk the edit has not reached yet.

// graphMayChange reports whether an edit to rel can change the resolved Go
// import graph. existed says the file is already on disk; before and after
// are its content on disk and as the edit would leave it.
//
// Only a Go file's header can: everything from its first byte (build
// constraints, package clause) through its last import declaration decides
// which package it belongs to, on which platforms, and what it imports. A
// file added or a module file (go.mod, go.sum, go.work, vendor/modules.txt)
// touched can too. Anything that does not parse is treated as changing the
// graph: this is a filter for the expensive answer, never a reason to skip
// it on doubt.
func graphMayChange(rel string, existed bool, before, after string) bool {
	switch path.Base(rel) {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	if rel == "vendor/modules.txt" || strings.HasSuffix(rel, "/vendor/modules.txt") {
		return true
	}
	if !strings.HasSuffix(rel, ".go") {
		return false
	}
	if !existed {
		return true
	}
	b, berr := goHeader(before)
	a, aerr := goHeader(after)
	return berr != nil || aerr != nil || a != b
}

// goHeader is src through its last import declaration, or through the
// package clause when it imports nothing.
func goHeader(src string) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return "", err
	}
	end := f.Name.End()
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT && g.End() > end {
			end = g.End()
		}
	}
	// Positions are 1-based offsets into a file that starts at base 1.
	return src[:int(end)-1], nil
}
