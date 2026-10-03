package kernel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPackage_importsOnlyPureStdlibAndReadsNoClock holds architecture §2: the
// kernel imports the standard library and kernel types only (no os, no
// os/exec), and time arrives in the event, never from time.Now.
func TestPackage_importsOnlyPureStdlibAndReadsNoClock(t *testing.T) {
	allowed := map[string]bool{"maps": true, "slices": true, "time": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, de := range entries {
		name := de.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); !allowed[p] {
				t.Errorf("%s imports %q: the kernel may import only %v", name, p, []string{"maps", "slices", "time"})
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" && (sel.Sel.Name == "Now" || sel.Sel.Name == "Since" || sel.Sel.Name == "Until") {
				t.Errorf("%s reads the clock with time.%s: time comes in the event", name, sel.Sel.Name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no production files found: the check read nothing")
	}
}
