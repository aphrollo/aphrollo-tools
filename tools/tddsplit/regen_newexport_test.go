package main

import (
	"bytes"
	"errors"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chainFixture is the unsplit package p carved into low (L0), mid (L1) and p
// (L2, the root): mid's Wrap reaches low's base, and the root's Use reaches
// mid's Wrap.
var chainFixture = map[string]string{
	"go.mod": "module example.com/fx\n\ngo 1.26\n",
	"p/a.go": "package p\n\nfunc base() int { return 1 }\n",
	"p/m.go": "package p\n\nfunc Wrap() int { return base() + 1 }\n",
	"p/b.go": "package p\n\nfunc Use() int { return Wrap() }\n",
	"p/b_test.go": `package p

import "testing"

func TestUse(t *testing.T) {
	if Use() != 2 {
		t.Fatal("Use")
	}
}
`,
	"tools/tddsplit/manifest.txt": `root p
[packages]
low L0 p/low
mid L1 p/mid
p   L2 p
[files]
a.go      low
b.go      p
b_test.go p
m.go      mid
`,
}

func TestGapFollowOn_DropsOnlyAnUndefinedSelectorIntoAGapPackage(t *testing.T) {
	gaps := map[string]bool{"mid": true}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"selector into a gap package", types.Error{Msg: "undefined: mid.Wrap"}, true},
		{"selector into another package", types.Error{Msg: "undefined: core.Wrap"}, false},
		{"bare undefined name", types.Error{Msg: "undefined: mid"}, false},
		{"a different message", types.Error{Msg: "cannot use mid.Wrap() as string value"}, false},
		{"not a type error", errors.New("undefined: mid.Wrap"), false},
	}
	for _, c := range cases {
		if got := gapFollowOn(c.err, gaps); got != c.want {
			t.Errorf("%s: gapFollowOn = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGapImporter_UnsafeIsNeverAGapAndAMissingPackageIs(t *testing.T) {
	gaps := map[string]bool{}
	imp := gapImporter{real: importer.ForCompiler(token.NewFileSet(), "gc", nil).(types.ImporterFrom), have: map[string]string{}, gaps: gaps}
	pkg, err := imp.Import("example.com/fx/p/mid")
	if err != nil {
		t.Fatalf("a package with no export data was refused: %v", err)
	}
	if pkg.Name() != "mid" || !pkg.Complete() || !gaps["mid"] {
		t.Errorf("gap package = %q complete=%v gaps=%v, want an empty complete \"mid\" recorded as a gap", pkg.Name(), pkg.Complete(), gaps)
	}
	if got, err := imp.Import("unsafe"); err != nil || got != types.Unsafe || gaps["unsafe"] {
		t.Errorf("unsafe imported as %v, err %v, gaps %v; want the real unsafe package and no gap", got, err, gaps)
	}
}

// TestRegen_ATypeErrorOfItsOwnStillRefusesPastTheGap keeps the tolerance to
// the missing import: a real error in the reassembled sources is refused.
func TestRegen_ATypeErrorOfItsOwnStillRefusesPastTheGap(t *testing.T) {
	repo := fixtureRepo(t, chainFixture)
	if out, err := runFixture(t, repo, "L0,L1"); err != nil {
		t.Fatalf("initial split: %v\n%s", err, out)
	}
	git(t, repo, "commit", "-q", "-m", "split")
	writeTree(t, repo, map[string]string{
		"p/low/a.go":  "package low\n\nfunc base() int { return 1 }\n\nfunc extra() int { return 5 }\n",
		"p/mid/m.go":  "package mid\n\nfunc Wrap() int { return base() + extra() + missing() }\n",
		"p/b_test.go": "package p\n\nimport (\n\t\"testing\"\n\n\t\"example.com/fx/p/mid\"\n)\n\nfunc TestUse(t *testing.T) { _ = mid.Wrap() }\n",
	})
	var out bytes.Buffer
	err := regenerate(Options{Repo: repo, Manifest: regenFixtureManifest(repo), Out: &out})
	if err == nil || !strings.Contains(err.Error(), "undefined: missing") {
		t.Fatalf("regenerate = %v, want a refusal naming undefined: missing\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "[write]") {
		t.Errorf("a refused run wrote a generated file:\n%s", out.String())
	}
}

// TestRegen_ResolvesANewReferenceWhenAnImportedPackageDoesNotCompileYet is the
// lane that adds low.extra and calls it from mid while a root test file
// imports mid directly: mid cannot compile until its forwarder for extra
// exists, so its export data is missing when -regen type-checks, and -regen
// alone must still write that forwarder.
func TestRegen_ResolvesANewReferenceWhenAnImportedPackageDoesNotCompileYet(t *testing.T) {
	repo := fixtureRepo(t, chainFixture)
	if out, err := runFixture(t, repo, "L0,L1"); err != nil {
		t.Fatalf("initial split: %v\n%s", err, out)
	}
	git(t, repo, "commit", "-q", "-m", "split")

	writeTree(t, repo, map[string]string{
		"p/low/a.go": "package low\n\nfunc base() int { return 1 }\n\nfunc extra() int { return 5 }\n",
		"p/mid/m.go": "package mid\n\nfunc Wrap() int { return base() + extra() }\n",
		"p/b_test.go": `package p

import (
	"testing"

	"example.com/fx/p/mid"
)

func TestUse(t *testing.T) {
	if Use() != mid.Wrap() {
		t.Fatal("Use")
	}
}
`,
	})

	var out bytes.Buffer
	if err := regenerate(Options{Repo: repo, Manifest: regenFixtureManifest(repo), Out: &out}); err != nil {
		t.Fatalf("regenerate: %v\n%s", err, out.String())
	}
	deps, err := os.ReadFile(filepath.Join(repo, "p/mid/deps_low.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deps), "func extra() int { return low.Extra() }") {
		t.Errorf("p/mid/deps_low.go lacks the forwarder for extra:\n%s", deps)
	}
	exp, err := os.ReadFile(filepath.Join(repo, "p/low/export.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exp), "func Extra() int { return extra() }") {
		t.Errorf("p/low/export.go lacks the export for extra:\n%s", exp)
	}
	goCmd(t, repo, nil, "vet", "./...")
}
