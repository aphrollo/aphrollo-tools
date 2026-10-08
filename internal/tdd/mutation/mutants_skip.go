package mutation

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Unproductive mutants as data. A call that cannot fail under its documented
// contract has an error test no test can drive, so the mutants of that test
// survive whatever the tests are. An entry of mutants-skip is
// `<import path>.<Func>` (crypto/rand.Read): the mutants of the comparison
// `err != nil` or `err == nil` on the error that call returns, in the `if`
// that tests it, are not run by the commit gate, and CI reports them as
// skipped. They are counted in every report, never dropped. Every other
// operator, in the condition or the body, is measured as usual.

// mutantSkipped is the status of a mutant a skip entry took out of the
// measurement.
const mutantSkipped = "skipped"

// mutantsSkipKey is the repo's own additions to the skip list.
const mutantsSkipKey = "mutants-skip"

// defaultMutantsSkip is what every repo skips: calls whose documentation
// promises they do not fail. crypto/rand.Read does not return an error since
// Go 1.24: it crashes the program instead. The list is kept to what a contract
// says, not what a box usually does.
var defaultMutantsSkip = []string{"crypto/rand.Read"}

// SkipList is the default list and the repo's own, each once, in that order.
func (c MutantsConfig) SkipList() []string {
	list := slices.Clone(defaultMutantsSkip)
	for _, entry := range c.Skip {
		if !slices.Contains(list, entry) {
			list = append(list, entry)
		}
	}
	return list
}

// checkSkipEntries refuses an entry that is not `<import path>.<Func>`: one
// that cannot match would skip nothing and read as if it did.
func checkSkipEntries(entries []string) error {
	for _, entry := range entries {
		if _, _, ok := splitSkipEntry(entry); !ok {
			return fmt.Errorf("%s entry %q is not <import path>.<Func>, like crypto/rand.Read", mutantsSkipKey, entry)
		}
	}
	return nil
}

// splitSkipEntry is the import path and the function an entry names.
func splitSkipEntry(entry string) (pkg, fn string, ok bool) {
	i := strings.LastIndex(entry, ".")
	if i < 1 || i == len(entry)-1 || strings.ContainsAny(entry, " \t") {
		return "", "", false
	}
	return entry[:i], entry[i+1:], true
}

// skippedOperators is the position of every comparison of a call's error
// result, in a file, that the commit run and CI leave out: `err != nil` or
// `err == nil` where err is assigned from a call on skips in the init of the
// `if` or by the statement just before it. Every other operator of the
// condition, and anything in a function literal, stays measured. A name that
// the function declares itself (a parameter, receiver, variable or range
// variable) is not the import, whatever it is called.
func skippedOperators(file *ast.File, skips []string) map[token.Pos]bool {
	if len(skips) == 0 {
		return nil
	}
	imports := map[string]string{} // name in this file -> import path
	for _, spec := range file.Imports {
		p := strings.Trim(spec.Path.Value, `"`)
		name := path.Base(p)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = p
	}
	skipped := map[token.Pos]bool{}
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		declared := declaredNames(fd)
		isSkipped := func(call *ast.CallExpr) bool {
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || declared[id.Name] {
				return false
			}
			for _, entry := range skips {
				if pkg, fn, ok := splitSkipEntry(entry); ok && imports[id.Name] == pkg && sel.Sel.Name == fn {
					return true
				}
			}
			return false
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BlockStmt:
				markIfsOf(n.List, isSkipped, skipped)
			case *ast.CaseClause:
				markIfsOf(n.Body, isSkipped, skipped)
			case *ast.CommClause:
				markIfsOf(n.Body, isSkipped, skipped)
			}
			return true
		})
	}
	return skipped
}

// markIfsOf marks the error comparisons of the ifs in a statement list, an if
// being judged with the statement before it.
func markIfsOf(list []ast.Stmt, isSkipped func(*ast.CallExpr) bool, skipped map[token.Pos]bool) {
	for i, stmt := range list {
		cur, ok := stmt.(*ast.IfStmt)
		if !ok {
			continue
		}
		before := ""
		if i > 0 {
			before = errAssignedFrom(list[i-1], isSkipped)
		}
		for ; cur != nil; cur = elseIf(cur) {
			name := before
			if cur.Init != nil {
				if got := errAssignedFrom(cur.Init, isSkipped); got != "" {
					name = got
				}
			}
			before = "" // the statement before the if is for the if alone
			if name != "" {
				markErrComparisons(cur.Cond, name, skipped)
			}
		}
	}
}

func elseIf(stmt *ast.IfStmt) *ast.IfStmt {
	next, _ := stmt.Else.(*ast.IfStmt)
	return next
}

// errAssignedFrom is the name of the last variable a statement assigns from a
// call that isSkipped, "" when it is not such an assignment.
func errAssignedFrom(stmt ast.Stmt, isSkipped func(*ast.CallExpr) bool) string {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
		return ""
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || !isSkipped(call) {
		return ""
	}
	if id, ok := assign.Lhs[len(assign.Lhs)-1].(*ast.Ident); ok && id.Name != "_" {
		return id.Name
	}
	return ""
}

// markErrComparisons marks `name != nil` and `name == nil` in cond, leaving
// function literals alone.
func markErrComparisons(cond ast.Expr, name string, skipped map[token.Pos]bool) {
	ast.Inspect(cond, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BinaryExpr:
			if (e.Op == token.NEQ || e.Op == token.EQL) && comparesToNil(e, name) {
				skipped[e.OpPos] = true
			}
		}
		return true
	})
}

// comparesToNil says whether e compares the identifier name with nil.
func comparesToNil(e *ast.BinaryExpr, name string) bool {
	is := func(x ast.Expr, want string) bool {
		id, ok := x.(*ast.Ident)
		return ok && id.Name == want
	}
	return (is(e.X, name) && is(e.Y, "nil")) || (is(e.X, "nil") && is(e.Y, name))
}

// declaredNames is every name the function declares: its receiver, parameters
// and results, and the variables of its body, function literals included. A
// scope-blind set, so a name declared anywhere in the function is never taken
// for an import in it.
func declaredNames(fd *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	fields := func(lists ...*ast.FieldList) {
		for _, list := range lists {
			if list == nil {
				continue
			}
			for _, f := range list.List {
				for _, id := range f.Names {
					names[id.Name] = true
				}
			}
		}
	}
	fields(fd.Recv, fd.Type.Params, fd.Type.Results)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			fields(n.Type.Params, n.Type.Results)
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						names[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				names[id.Name] = true
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				for _, x := range []ast.Expr{n.Key, n.Value} {
					if id, ok := x.(*ast.Ident); ok {
						names[id.Name] = true
					}
				}
			}
		case *ast.TypeSwitchStmt:
			if assign, ok := n.Assign.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						names[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return names
}

// markSkipped sets the status of the outcomes at skipped operators to
// mutantSkipped, so a report counts them and no verdict judges them.
func markSkipped(root string, outcomes []MutantOutcome, skips []string) []MutantOutcome {
	out := slices.Clone(outcomes)
	at := map[string]map[[2]int]bool{}
	for i, o := range out {
		if o.Status == mutantSkipped {
			continue
		}
		positions, seen := at[o.File]
		if !seen {
			positions = skippedPositions(root, o.File, skips)
			at[o.File] = positions
		}
		if positions[[2]int{o.Line, o.Col}] {
			out[i].Status = mutantSkipped
			out[i].Note = skippedNote
		}
	}
	return out
}

// skippedNote is why a skipped mutant is not judged.
const skippedNote = "an operator of a call on the mutants-skip list"

// skippedPositions are the line and column of the skipped operators of file.
func skippedPositions(root, file string, skips []string) map[[2]int]bool {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil
	}
	fset, parsed, _ := parseFuncSpans(src)
	if parsed == nil {
		return nil
	}
	out := map[[2]int]bool{}
	for pos := range skippedOperators(parsed, skips) {
		p := fset.Position(pos)
		out[[2]int{p.Line, p.Column}] = true
	}
	return out
}
