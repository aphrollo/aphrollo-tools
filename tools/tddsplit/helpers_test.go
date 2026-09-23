package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helperFixture is a package p whose tests share helpers across the split:
// a.go and its test move to p/low, b.go and its test stay. The shared
// helpers forward to tt, the prep package standing in for tddtest; one of
// them (requireParsed) hands tt an adapter (parsed) that reads a function
// declared in the moved file.
var helperFixture = map[string]string{
	"go.mod": "module example.com/fx\n\ngo 1.26\n",
	"p/internal/tt/tt.go": `package tt

import "testing"

// Write returns s marked as written.
func Write(t *testing.T, s string) string { t.Helper(); return s + "!" }

// Require fails t unless ok(s).
func Require(t *testing.T, s string, ok func(string) bool) {
	t.Helper()
	if !ok(s) {
		t.Fatalf("%q not accepted", s)
	}
}
`,
	"p/a.go": "package p\n\nfunc parse(s string) (string, bool) { return s, s != \"\" }\n",
	"p/a_test.go": `package p

import (
	"testing"

	"example.com/fx/p/internal/tt"
)

func TestLow_UsesTheSharedHelpers(t *testing.T) {
	if got := write(t, "a"); got != "a!" {
		t.Fatalf("write = %q", got)
	}
	requireParsed(t, "x")
}

func mk(t *testing.T) string { t.Helper(); return tt.Write(t, "m") }
`,
	"p/b.go": "package p\n\n// Use reaches across the split.\nfunc Use() bool { _, ok := parse(\"u\"); return ok }\n",
	"p/b_test.go": `package p

import (
	"testing"

	"example.com/fx/p/internal/tt"
)

func write(t *testing.T, s string) string { t.Helper(); return tt.Write(t, s) }

func requireParsed(t *testing.T, s string) { t.Helper(); tt.Require(t, s, parsed) }

func parsed(s string) bool {
	_, ok := parse(s)
	return ok
}

func TestP_UsesAMovedHelper(t *testing.T) {
	if got := mk(t); got != "m!" {
		t.Fatalf("mk = %q", got)
	}
}
`,
	"tools/tddsplit/manifest.txt": `root p
[packages]
tt  prep p/internal/tt
low L0   p/low
p   L1   p
[files]
a.go      low
a_test.go low
b.go      p
b_test.go p
`,
}

// A test that moves keeps calling the shared helpers it called before, by
// the same names, so its body stays byte-identical: each package whose tests
// call a helper declared on the other side of the split gets a generated
// copy of that helper, and the helpers it reaches, in
// tddtest_wrappers_test.go.
func TestRun_SharedTestHelpersFollowTheTestsThatCallThem(t *testing.T) {
	repo := fixtureRepo(t, helperFixture)
	out, err := runFixture(t, repo, "L0")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(out, "test helper") {
		t.Errorf("a helper the generator can carry was reported instead:\n%s", out)
	}
	for path, want := range map[string][]string{
		"p/low/tddtest_wrappers_test.go": {genHeader, "func write(", "func requireParsed(", "func parsed("},
		"p/tddtest_wrappers_test.go":     {genHeader, "func mk("},
	} {
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("%s was not generated: %v", path, err)
			continue
		}
		for _, w := range want {
			if !strings.Contains(string(data), w) {
				t.Errorf("%s lacks %q:\n%s", path, w, data)
			}
		}
	}
	goCmd(t, repo, nil, "vet", "./...")
	goCmd(t, repo, nil, "test", "-count=1", "./...")
}

// A shared test constant forwarding to tddtest crosses the split the same
// way a helper func does: the moved test keeps naming it, and the package it
// moved into gets a copy of its declaration.
func TestRun_SharedTestConstantsFollowTheTestsThatNameThem(t *testing.T) {
	files := map[string]string{}
	for k, v := range helperFixture {
		files[k] = v
	}
	files["p/internal/tt/leaf.go"] = "package tt\n\n// Leaf is a shared fixture value.\nconst Leaf = 7\n"
	files["p/b_test.go"] += "\nconst (\n\tfixtureLeaf = tt.Leaf\n\tunrelated   = 3\n)\n"
	files["p/a_test.go"] += "\nfunc TestLow_NamesTheSharedConstant(t *testing.T) {\n\tif fixtureLeaf != 7 {\n\t\tt.Fatal(fixtureLeaf)\n\t}\n}\n"
	repo := fixtureRepo(t, files)
	out, err := runFixture(t, repo, "L0")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(out, "test helper") {
		t.Errorf("a constant the generator can carry was reported instead:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(repo, "p/low/tddtest_wrappers_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "const fixtureLeaf = tt.Leaf") || strings.Contains(string(data), "unrelated") {
		t.Errorf("want exactly fixtureLeaf carried, as `const fixtureLeaf = tt.Leaf`:\n%s", data)
	}
	goCmd(t, repo, nil, "vet", "./...")
	goCmd(t, repo, nil, "test", "-count=1", "./...")
}

// A helper whose body reaches an unexported field of a type that lands in
// another package would not compile once copied: the copy is reported, and
// never emitted.
func TestAnalyze_ReportsAndDropsAHelperCopyThatReadsAnUnexportedField(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/fx\n\ngo 1.26\n",
		"p/a.go":      "package p\n\ntype pair struct{ k string }\n\nfunc (p pair) key() string { return p.k }\n",
		"p/a_test.go": "package p\n\nfunc mkPair() pair { return pair{k: \"x\"} }\n\nfunc pairKey() string { return mkPair().key() }\n",
		"p/b.go":      "package p\n\n// Use keeps pair reachable from p.\nfunc Use() pair { return pair{} }\n",
		"p/b_test.go": "package p\n\nimport \"testing\"\n\nfunc TestP_BuildsAPair(t *testing.T) {\n\t_ = mkPair()\n\t_ = pairKey()\n}\n",
	}
	manifest := "root p\n[packages]\nlow L0 p/low\np L1 p\n[files]\na.go low\na_test.go low\nb.go p\nb_test.go p\n"
	a := analyzeFixture(t, files, manifest, "L0")
	var hits []string
	for _, r := range a.Reports {
		if strings.Contains(r, "unexported") && (strings.Contains(r, "mkPair") || strings.Contains(r, "pairKey")) {
			hits = append(hits, r)
		}
	}
	if len(hits) != 2 || !strings.Contains(strings.Join(hits, "\n"), " k ") || !strings.Contains(strings.Join(hits, "\n"), " key ") {
		t.Errorf("want mkPair reported for field k and pairKey for method key; reports:\n%s", strings.Join(a.Reports, "\n"))
	}
	if gen := string(a.Generated["p/tddtest_wrappers_test.go"]); strings.Contains(gen, "mkPair") || strings.Contains(gen, "pairKey") {
		t.Errorf("a copy that cannot compile was emitted:\n%s", gen)
	}
}

// A shared test var forwarding to tddtest crosses like a const when nothing
// writes it: the moved test keeps naming it, and its new package gets a copy
// of the declaration. A test var some test assigns is state, not a shared
// value; two copies would drift apart, so it is reported and never copied.
func TestRun_SharedTestVarsFollowTheTestsThatNameThem(t *testing.T) {
	files := map[string]string{}
	for k, v := range helperFixture {
		files[k] = v
	}
	files["p/internal/tt/keys.go"] = "package tt\n\n// Keys is a shared fixture list.\nvar Keys = []string{\"a\", \"b\"}\n"
	files["p/b_test.go"] += "\nvar sharedKeys = tt.Keys\n\nvar counter = 0\n\nfunc TestP_Counts(t *testing.T) { counter++ }\n"
	files["p/a_test.go"] += "\nfunc TestLow_NamesTheSharedVar(t *testing.T) {\n\tif len(sharedKeys) != 2 {\n\t\tt.Fatal(sharedKeys)\n\t}\n}\n"
	repo := fixtureRepo(t, files)
	out, err := runFixture(t, repo, "L0")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(out, "sharedKeys") {
		t.Errorf("a var the generator can carry was reported instead:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(repo, "p/low/tddtest_wrappers_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "var sharedKeys = tt.Keys") {
		t.Errorf("want `var sharedKeys = tt.Keys` carried:\n%s", data)
	}
	goCmd(t, repo, nil, "vet", "./...")
	goCmd(t, repo, nil, "test", "-count=1", "./...")
}

func TestAnalyze_ReportsAWrittenTestVarInsteadOfCopyingIt(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/fx\n\ngo 1.26\n",
		"p/a.go":      "package p\n\nfunc base() int { return 1 }\n",
		"p/a_test.go": "package p\n\nimport \"testing\"\n\nfunc TestLow_ReadsTheCounter(t *testing.T) { _ = counter + base(); seen.Store(\"k\", 1) }\n",
		"p/b.go":      "package p\n\n// Use keeps base reachable.\nfunc Use() int { return base() }\n",
		"p/b_test.go": "package p\n\nimport (\n\t\"sync\"\n\t\"testing\"\n)\n\nvar counter = 0\n\nvar seen = sync.Map{}\n\nfunc TestP_Counts(t *testing.T) { counter++ }\n",
	}
	manifest := "root p\n[packages]\nlow L0 p/low\np L1 p\n[files]\na.go low\na_test.go low\nb.go p\nb_test.go p\n"
	a := analyzeFixture(t, files, manifest, "L0")
	for _, name := range []string{"counter", "seen"} {
		found := false
		for _, r := range a.Reports {
			if strings.Contains(r, "test var "+name+" ") {
				found = true
			}
		}
		if !found {
			t.Errorf("no report says test var %s cannot be carried; reports:\n%s", name, strings.Join(a.Reports, "\n"))
		}
		if gen := string(a.Generated["p/low/tddtest_wrappers_test.go"]); strings.Contains(gen, "var "+name) {
			t.Errorf("test var %s, which holds state, was copied:\n%s", name, gen)
		}
	}
}

// A helper whose signature or body names something of a HIGHER package than
// the one it would be copied into cannot compile there: no alias reaches
// upward. The copy is reported and never emitted, and neither is a helper
// that relies on such a copy.
func TestAnalyze_NeverCopiesAHelperThatReachesUpward(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/fx\n\ngo 1.26\n",
		"p/a.go":      "package p\n\nfunc base() int { return 1 }\n",
		"p/a_test.go": "package p\n\nimport \"testing\"\n\nfunc TestLow_UsesHelpers(t *testing.T) {\n\t_ = decideIt()\n\t_ = viaDecide()\n\t_ = sigOnly(nil)\n\t_ = base()\n}\n",
		"p/b.go":      "package p\n\n// Decision is a high-level verdict.\ntype Decision struct{ OK bool }\n\n// Decide decides.\nfunc Decide() Decision { return Decision{OK: base() == 1} }\n",
		"p/b_test.go": "package p\n\nfunc decideIt() bool { return Decide().OK }\n\nfunc viaDecide() bool { return decideIt() }\n\nfunc sigOnly(d *Decision) bool { return d == nil }\n",
	}
	manifest := "root p\n[packages]\nlow L0 p/low\np L1 p\n[files]\na.go low\na_test.go low\nb.go p\nb_test.go p\n"
	a := analyzeFixture(t, files, manifest, "L0")
	gen := string(a.Generated["p/low/tddtest_wrappers_test.go"])
	for _, name := range []string{"decideIt", "viaDecide", "sigOnly"} {
		if strings.Contains(gen, "func "+name+"(") {
			t.Errorf("helper %s, which reaches package p above low, was copied into low:\n%s", name, gen)
		}
		found := false
		for _, r := range a.Reports {
			if strings.Contains(r, "test helper "+name+" ") && strings.Contains(r, "not carried") {
				found = true
			}
		}
		if !found {
			t.Errorf("no report says helper %s is not carried; reports:\n%s", name, strings.Join(a.Reports, "\n"))
		}
	}
}
