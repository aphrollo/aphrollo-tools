package render

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPackage_importsNoBoxAndReadsNoClock holds architecture §2: render is
// pure. It imports the standard library's text and encoding packages and the
// kernel's types, and nothing that reads the box: no os, no os/exec, no time
// (a duration arrives as milliseconds), no store.
func TestPackage_importsNoBoxAndReadsNoClock(t *testing.T) {
	allowed := map[string]bool{
		"bytes": true, "crypto/sha256": true, "encoding/hex": true, "encoding/json": true, "regexp": true,
		"slices": true, "strconv": true, "strings": true, "unicode": true,
		"github.com/aphrollo/aphrollo-tools/internal/kernel": true,
	}
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
			if p := strings.Trim(imp.Path.Value, `"`); !allowed[p] {
				t.Errorf("%s imports %q: render may import only the text, encoding and kernel packages", name, p)
			}
		}
	}
	if checked < 4 {
		t.Fatalf("checked %d production files, want the package's 5: the check read nothing", checked)
	}
}
