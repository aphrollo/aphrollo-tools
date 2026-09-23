package main

import (
	"strings"
	"testing"
)

// A generated func whose name starts with Test reads as a Go test: vet
// refuses one in a _test.go file ("wrong signature"), and a law that scans
// every .go file for unnamed tests refuses one anywhere. The generator
// reports each one and emits none of them; the source symbol is renamed
// instead.
func TestAnalyze_NeverEmitsATestPrefixedFunc(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/fx\n\ngo 1.26\n",
		"p/a.go":      "package p\n\nfunc testLine(s string) bool { return s != \"\" }\n\n// TestNotes is exported already, and only tests above use it.\nfunc TestNotes(s string) []string { return []string{s} }\n",
		"p/b.go":      "package p\n\n// Use reaches testLine across the split.\nfunc Use() bool { return testLine(\"x\") }\n",
		"p/b_test.go": "package p\n\nimport \"testing\"\n\nfunc TestNotes_OneNote(t *testing.T) {\n\tif len(TestNotes(\"x\")) != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n",
	}
	manifest := "root p\n[packages]\nlow L0 p/low\np L1 p\n[files]\na.go low\nb.go p\nb_test.go p\n"
	a := analyzeFixture(t, files, manifest, "L0")
	for path, body := range a.Generated {
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "func Test") {
				t.Errorf("%s emits a Test-prefixed func: %s", path, line)
			}
		}
	}
	for _, name := range []string{"testLine", "TestNotes"} {
		found := false
		for _, r := range a.Reports {
			if strings.Contains(r, name) && strings.Contains(r, "Test-prefixed") {
				found = true
			}
		}
		if !found {
			t.Errorf("no report names %s as a Test-prefixed alias to rename; reports:\n%s", name, strings.Join(a.Reports, "\n"))
		}
	}
}
