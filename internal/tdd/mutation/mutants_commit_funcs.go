package mutation

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// A function is the unit the commit-time run selects tests by. A line-keyed
// map would be invalidated by the very commit it serves, since the lines
// shift; a function's name and receiver do not move when its body is edited.

// funcSpan is one function declaration's key and the lines it covers.
type funcSpan struct {
	Key        string
	Start, End int
}

// parseFuncSpans parses src and answers its function declarations' spans, or
// nil for a source that does not parse: nothing can be keyed in it.
func parseFuncSpans(src []byte) (*token.FileSet, *ast.File, []funcSpan) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, nil
	}
	var spans []funcSpan
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		spans = append(spans, funcSpan{
			Key:   funcKey(fd),
			Start: fset.Position(fd.Pos()).Line,
			End:   fset.Position(fd.End()).Line,
		})
	}
	return fset, file, spans
}

// funcKey is a declaration's name inside its package: `Name` for a function,
// `Recv.Name` for a method, the receiver's type name without its pointer or
// type parameters.
func funcKey(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return receiverName(fd.Recv.List[0].Type) + "." + fd.Name.Name
}

// receiverName is the type name a receiver expression names.
func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.ParenExpr:
		return receiverName(e.X)
	case *ast.IndexExpr:
		return receiverName(e.X)
	case *ast.IndexListExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// enclosingFunc is the key of the function whose lines hold line, "" when
// none does (a package-level declaration).
func enclosingFunc(spans []funcSpan, line int) string {
	for _, s := range spans {
		if s.Start <= line && line <= s.End {
			return s.Key
		}
	}
	return ""
}
