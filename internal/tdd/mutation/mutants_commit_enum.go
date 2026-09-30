package mutation

import (
	"cmp"
	"go/ast"
	"go/token"
	"slices"
)

// commitMutant is one mutant on a line the commit adds: where it sits, what
// mutates it, and the function whose tests judge it.
type commitMutant struct {
	File     string
	Line     int
	Col      int
	Mutation string
	Func     string
}

// commitMutators are the mutators gremlins enables by default, which are the
// ones CI's mutants-verdict measures. The token each rewrites, and to what,
// is gremlinsTokenMutations, gremlins' own table.
var commitMutators = []string{
	"ARITHMETIC_BASE", "CONDITIONALS_BOUNDARY", "CONDITIONALS_NEGATION",
	"INCREMENT_DECREMENT", "INVERT_NEGATIVES",
}

// enumerateCommitMutants lists the mutants of src on the lines in added, in
// position order. gremlins picks its mutants by the operator token of five
// node kinds (assignment, binary and unary expressions, branch and
// inc/dec statements), each token to the mutators that rewrite it; this walks
// the same nodes with the same table, without gremlins' whole-module coverage
// gather, which takes minutes on this repo and cannot sit on a commit's path.
//
// A position outside every function declaration has no function to select
// tests for and is left out, as CI's judge exempts it. A source that does
// not parse has no mutants.
func enumerateCommitMutants(file string, src []byte, added map[int]bool) []commitMutant {
	if len(added) == 0 {
		return nil
	}
	fset, parsed, spans := parseFuncSpans(src)
	if parsed == nil {
		return nil
	}
	var out []commitMutant
	ast.Inspect(parsed, func(n ast.Node) bool {
		tok, pos := operatorOf(n)
		if pos == token.NoPos {
			return true
		}
		p := fset.Position(pos)
		fn := enclosingFunc(spans, p.Line)
		if !added[p.Line] || fn == "" {
			return true
		}
		for _, mutator := range commitMutators {
			if _, ok := gremlinsTokenMutations[mutator][tok]; ok {
				out = append(out, commitMutant{File: file, Line: p.Line, Col: p.Column, Mutation: mutator, Func: fn})
			}
		}
		return true
	})
	slices.SortFunc(out, func(a, b commitMutant) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col), cmp.Compare(a.Mutation, b.Mutation))
	})
	return out
}

// operatorOf is the token gremlins would mutate in n and its position, or
// token.NoPos for a node it does not look at.
func operatorOf(n ast.Node) (token.Token, token.Pos) {
	switch n := n.(type) {
	case *ast.AssignStmt:
		return n.Tok, n.TokPos
	case *ast.BinaryExpr:
		return n.Op, n.OpPos
	case *ast.BranchStmt:
		return n.Tok, n.TokPos
	case *ast.IncDecStmt:
		return n.Tok, n.TokPos
	case *ast.UnaryExpr:
		return n.Op, n.OpPos
	}
	return token.ILLEGAL, token.NoPos
}
