package mutation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
)

// Where Go coverage can count a position at all. gremlins files a mutant NOT
// COVERED when no executed coverage block contains it, and Go's cover tool
// (cmd/cover, addCounters and statementBoundary) leaves some positions
// outside every block however often they run:
//
//   - anything outside a function body: a package-level var or const
//     initializer, and the body of a function named `_`;
//   - a case clause's expressions and a select clause's communication, which
//     sit between the switch's opening brace and the clause's colon, where
//     the clause's own counter starts;
//   - the rest of a statement after its first function literal: the block
//     ends at that literal's body, and the next one starts at the next
//     statement.
//
// The model below replays those rules over the syntax tree, so the gate can
// tell a gap coverage cannot see from code no test runs. It works on the
// chain of nodes from the file down to the one that owns the mutated
// operator, found by the operator's own line and column, so every decision
// is about which node holds which rather than about offsets.
// testdata/covershape holds a fully exercised module and gremlins' own
// report over it, and the model is held to agree with the tool there.

// coverShape is where one position stands to Go coverage.
type coverShape int

const (
	// shapeCoverable is a position some coverage block contains: a NOT
	// COVERED there means no test ran it.
	shapeCoverable coverShape = iota
	// shapeCoverGap is inside a function but in a gap no block contains.
	// The code may well run; only a run of the mutant itself can say.
	shapeCoverGap
	// shapeOutsideFunc is outside any instrumented function body. Go
	// coverage never attributes such a line to a test.
	shapeOutsideFunc
)

func (s coverShape) String() string {
	switch s {
	case shapeCoverGap:
		return "cover-gap"
	case shapeOutsideFunc:
		return "outside-func"
	}
	return "coverable"
}

// coverShapeAt classifies the operator at line:col (1-based, col in bytes,
// the way gremlins reports it) of the Go source src. A file that does not
// parse, or a position holding no operator, is coverable: the model only
// ever takes a refusal away, and only where it can show why.
func coverShapeAt(src []byte, line, col int) coverShape {
	chain := pathToOperator(src, line, col)
	if chain == nil {
		return shapeCoverable
	}
	body, inFunc := innermostBody(chain)
	if !inFunc {
		return shapeOutsideFunc
	}
	if stmtCovered(chain, body+1) {
		return shapeCoverable
	}
	return shapeCoverGap
}

// pathToOperator is the chain of nodes from the file to the one whose
// operator token starts at line:col, nil when src does not parse or no
// operator starts there.
func pathToOperator(src []byte, line, col int) []ast.Node {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var stack, found []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if p := fset.Position(operatorPos(n)); p.Line == line && p.Column == col {
			found = append([]ast.Node(nil), stack...)
		}
		return true
	})
	return found
}

// operatorPos is where the token a gremlins mutator rewrites sits in n, for
// the node kinds that carry one; NoPos, which is on no line, for the rest.
func operatorPos(n ast.Node) token.Pos {
	switch n := n.(type) {
	case *ast.BinaryExpr:
		return n.OpPos
	case *ast.UnaryExpr:
		return n.OpPos
	case *ast.AssignStmt:
		return n.TokPos
	case *ast.IncDecStmt:
		return n.TokPos
	case *ast.BranchStmt:
		return n.TokPos
	}
	return token.NoPos
}

// innermostBody is the index in chain of the body of the innermost function
// declaration or literal the operator sits in. inFunc is false when it sits
// in none — or inside a function named `_`, which cmd/cover never
// instruments, literals within it included.
func innermostBody(chain []ast.Node) (body int, inFunc bool) {
	for i, n := range chain[:len(chain)-1] {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Name.Name == "_" {
				return 0, false
			}
			if chain[i+1] == ast.Node(n.Body) {
				body, inFunc = i+1, true
			}
		case *ast.FuncLit:
			if chain[i+1] == ast.Node(n.Body) {
				body, inFunc = i+1, true
			}
		}
	}
	return body, inFunc
}

// stmtCovered answers for the statement at chain[i], one element of a
// statement list that starts coverage blocks: counted when the operator is
// in the part of it before its block boundary, otherwise counted only when
// a nested statement list, which starts blocks of its own, holds it. A
// nested block, a loop's or an if's body and a plain else are such lists,
// reached through the BlockStmt case; an else-if is a statement of its own.
func stmtCovered(chain []ast.Node, i int) bool {
	target := chain[len(chain)-1]
	if chain[i] == target {
		return !funcLitBefore(target, chain[i])
	}
	next := chain[i+1]
	switch s := chain[i].(type) {
	case *ast.LabeledStmt, *ast.BlockStmt:
		return stmtCovered(chain, i+1)
	case *ast.IfStmt:
		if next == ast.Node(s.Body) || next == s.Else {
			return stmtCovered(chain, i+1)
		}
		return !funcLitBefore(target, s.Init, s.Cond)
	case *ast.ForStmt:
		if next == ast.Node(s.Body) {
			return stmtCovered(chain, i+1)
		}
		return !funcLitBefore(target, s.Init, s.Cond, s.Post)
	case *ast.RangeStmt:
		if next == ast.Node(s.Body) {
			return stmtCovered(chain, i+1)
		}
		return !funcLitBefore(target, s.X)
	case *ast.SwitchStmt:
		if next == ast.Node(s.Body) {
			return clauseCovered(chain, i+2)
		}
		return !funcLitBefore(target, s.Init, s.Tag)
	case *ast.TypeSwitchStmt:
		if next == ast.Node(s.Body) {
			return clauseCovered(chain, i+2)
		}
		return !funcLitBefore(target, s.Init)
	case *ast.SelectStmt:
		return clauseCovered(chain, i+2)
	}
	return !funcLitBefore(target, chain[i])
}

// clauseCovered answers for a case or select clause at chain[i]: its body's
// statements start blocks, and its expressions or communication are in
// none.
func clauseCovered(chain []ast.Node, i int) bool {
	var body []ast.Stmt
	switch c := chain[i].(type) {
	case *ast.CaseClause:
		body = c.Body
	case *ast.CommClause:
		body = c.Body
	}
	for _, s := range body {
		if chain[i+1] == ast.Node(s) {
			return stmtCovered(chain, i+1)
		}
	}
	return false
}

// funcLitBefore reports whether a function literal comes ahead of target's
// operator in parts, taken in source order: where cmd/cover ends the block,
// so everything after it is in none. Absent parts are skipped.
func funcLitBefore(target ast.Node, parts ...ast.Node) bool {
	lit, reached := false, false
	for _, part := range parts {
		if part == nil {
			continue
		}
		ast.Inspect(part, func(n ast.Node) bool {
			if lit || reached {
				return false
			}
			if n == target {
				reached = true
				lit = containsFuncLit(operandsBefore(target)...)
				return false
			}
			_, isLit := n.(*ast.FuncLit)
			lit = isLit
			return true
		})
	}
	return lit
}

// operandsBefore is what precedes the operator inside the node that owns
// it: a binary expression's left side, an assignment's left-hand side, an
// increment's operand. A unary operator or a branch keyword comes first.
func operandsBefore(n ast.Node) []ast.Node {
	switch n := n.(type) {
	case *ast.BinaryExpr:
		return []ast.Node{n.X}
	case *ast.IncDecStmt:
		return []ast.Node{n.X}
	case *ast.AssignStmt:
		var lhs []ast.Node
		for _, e := range n.Lhs {
			lhs = append(lhs, e)
		}
		return lhs
	}
	return nil
}

// containsFuncLit reports a function literal anywhere in nodes.
func containsFuncLit(nodes ...ast.Node) bool {
	found := false
	for _, n := range nodes {
		ast.Inspect(n, func(c ast.Node) bool {
			if _, ok := c.(*ast.FuncLit); ok {
				found = true
			}
			return !found
		})
	}
	return found
}

// stringConcatAt reports whether the `+` at line:col belongs to a chain of
// additions with a string literal among its operands: ARITHMETIC_BASE there
// turns a concatenation into an operator strings do not have, and the
// mutant never compiles.
func stringConcatAt(src []byte, line, col int) bool {
	chain := pathToOperator(src, line, col)
	if chain == nil {
		return false
	}
	root := chain[len(chain)-1]
	for _, n := range slices.Backward(chain[:len(chain)-1]) {
		if !isConcatLink(n) {
			break
		}
		root = n
	}
	return isConcatLink(chain[len(chain)-1]) && hasStringLeaf(root)
}

// isConcatLink is a node an addition chain passes through: another `+`, or
// the parentheses around one.
func isConcatLink(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.BinaryExpr:
		return n.Op == token.ADD
	case *ast.ParenExpr:
		return true
	}
	return false
}

// hasStringLeaf reports a string literal among the operands of the addition
// chain rooted at n.
func hasStringLeaf(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.BinaryExpr:
		return n.Op == token.ADD && (hasStringLeaf(n.X) || hasStringLeaf(n.Y))
	case *ast.ParenExpr:
		return hasStringLeaf(n.X)
	case *ast.BasicLit:
		return n.Kind == token.STRING
	}
	return false
}
