package main

import (
	"os"
	"path/filepath"
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

// A test helper that is a type alias forwarding to tddtest crosses like a
// func: the copy names the same type, so values flow between packages
// unchanged. A defined type declared in a test file would become a second,
// distinct type; it is reported and never copied.
func TestRun_SharedTestTypeAliasesFollowTheTestsThatNameThem(t *testing.T) {
	files := map[string]string{}
	for k, v := range helperFixture {
		files[k] = v
	}
	files["p/internal/tt/rec.go"] = "package tt\n\n// Rec is a shared record.\ntype Rec struct{ N int }\n"
	files["p/b_test.go"] += "\ntype rec = tt.Rec\n\nfunc mkRec(n int) rec { return rec{N: n} }\n\ntype box struct{ n int }\n"
	files["p/a_test.go"] += "\nfunc TestLow_NamesTheSharedType(t *testing.T) {\n\tvar r rec = mkRec(2)\n\tif r.N != 2 {\n\t\tt.Fatal(r)\n\t}\n\t_ = box{}\n}\n"
	repo := fixtureRepo(t, files)
	out, _ := runFixture(t, repo, "L0")
	if !strings.Contains(out, "box") || !strings.Contains(out, "distinct type") {
		t.Errorf("the defined test type box was not reported as uncarriable:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(repo, "p/low/tddtest_wrappers_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type rec = tt.Rec", "func mkRec(n int) rec"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("want %q carried:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "type box") {
		t.Errorf("the defined type box was copied:\n%s", data)
	}
}

// A helper copy that assigns, or takes the address of, a var landing in
// another package would write the consumer's alias, not the var: a func
// seam's alias is a call-through func that cannot be assigned at all. The
// copy is reported and never emitted.
func TestAnalyze_NeverCopiesAHelperThatWritesAVarAcrossTheSplit(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/fx\n\ngo 1.26\n",
		"p/a.go":      "package p\n\nvar probe = func() bool { return true }\n\nfunc viaProbe() bool { return probe() }\n",
		"p/a_test.go": "package p\n\nfunc stubByAddress(fn func() bool) func() { prev := probe; set(&probe, fn); return func() { probe = prev } }\n\nfunc set(p *func() bool, fn func() bool) { *p = fn }\n\nfunc stubDirect(fn func() bool) { probe = fn }\n",
		"p/b.go":      "package p\n\n// Use reaches across the split.\nfunc Use() bool { return viaProbe() }\n",
		"p/b_test.go": "package p\n\nimport \"testing\"\n\nfunc TestP_Stubs(t *testing.T) {\n\tdefer stubByAddress(func() bool { return false })()\n\tstubDirect(func() bool { return true })\n}\n",
	}
	manifest := "root p\n[packages]\nlow L0 p/low\np L1 p\n[files]\na.go low\na_test.go low\nb.go p\nb_test.go p\n"
	a := analyzeFixture(t, files, manifest, "L0")
	gen := string(a.Generated["p/tddtest_wrappers_test.go"])
	for _, name := range []string{"stubByAddress", "stubDirect"} {
		if strings.Contains(gen, "func "+name+"(") {
			t.Errorf("helper %s, which writes low's probe, was copied into p:\n%s", name, gen)
		}
		found := false
		for _, r := range a.Reports {
			if strings.Contains(r, "test helper "+name+" ") && strings.Contains(r, "writes") {
				found = true
			}
		}
		if !found {
			t.Errorf("no report says %s writes a var across the split; reports:\n%s", name, strings.Join(a.Reports, "\n"))
		}
	}
}
