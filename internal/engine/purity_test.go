package engine

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPackage_importsNoProcessOrFileAccess holds architecture §2 for the
// engine: it decides and saves through the Store interface and returns effects
// for an adapter to run, so it imports no os, os/exec, net or syscall.
func TestPackage_importsNoProcessOrFileAccess(t *testing.T) {
	banned := []string{"os", "os/exec", "syscall", "net", "net/http", "io/ioutil", "path/filepath"}
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
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, b := range banned {
				if p == b {
					t.Errorf("%s imports %q: the engine returns effects, it never runs them", name, p)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no production files found: the check read nothing")
	}
}
