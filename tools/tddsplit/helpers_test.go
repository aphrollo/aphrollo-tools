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
