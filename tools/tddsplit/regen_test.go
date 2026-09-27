package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// regenFixture is the unsplit package p that regenFixtureRepo carves into
// low (L0) and p (L1, the root): a.go/a_test.go move to p/low, b.go stays
// and reaches every kind of alias low.Package's own tests cover already
// (const, seam var, read-only var, struct field, variadic func).
var regenFixture = map[string]string{
	"go.mod": "module example.com/fx\n\ngo 1.26\n",
	"p/a.go": `package p

import "strings"

const limit = 3

var hook = func(n int) bool { return n > limit }

var names = []string{"a"}

type thing struct{ name string }

func helper(s string, rest ...string) (string, error) { return s + strings.Join(rest, ""), nil }
`,
	"p/a_test.go": `package p

import "testing"

func TestHook_StubIsSeen(t *testing.T) {
	old := hook
	hook = func(int) bool { return true }
	defer func() { hook = old }()
	if !hook(0) {
		t.Fatal("stub not seen")
	}
}
`,
	"p/b.go": `package p

func Use() bool {
	t := thing{name: "x"}
	_, _ = helper(t.name)
	_ = names
	return hook(limit)
}
`,
	"tools/tddsplit/manifest.txt": `root p
[packages]
low L0 p/low
p   L1 p
[files]
a.go      low
a_test.go low
b.go      p
`,
}

// regenFixtureRepo builds regenFixture, carves it with the ordinary -levels
// mode and commits the result, so every regen test starts from a tree the
// drift test would call clean: every generated file already matches its
// committed sources.
func regenFixtureRepo(t *testing.T) string {
	t.Helper()
	repo := fixtureRepo(t, regenFixture)
	if out, err := runFixture(t, repo, "L0"); err != nil {
		t.Fatalf("initial split: %v\n%s", err, out)
	}
	git(t, repo, "commit", "-q", "-m", "split")
	return repo
}

func regenFixtureManifest(repo string) string {
	return filepath.Join(repo, "tools/tddsplit/manifest.txt")
}

func TestRegen_WritesGeneratedFilesFromDirtyEditsWithNoCommit(t *testing.T) {
	repo := regenFixtureRepo(t)
	head := git(t, repo, "rev-parse", "HEAD")

	// A working-tree-only edit (never committed): low gains a new symbol,
	// and the root's b.go starts using it, which needs a new alias pair in
	// the ALREADY-existing p/api_low.go and p/low/export.go.
	writeTree(t, repo, map[string]string{
		"p/low/a.go": `package low

import "strings"

const limit = 3

var hook = func(n int) bool { return n > limit }

var names = []string{"a"}

type thing struct{ name string }

func helper(s string, rest ...string) (string, error) { return s + strings.Join(rest, ""), nil }

func extra() int { return limit + 1 }
`,
		"p/b.go": `package p

func Use() bool {
	t := thing{name: "x"}
	_, _ = helper(t.name)
	_ = names
	_ = extra()
	return hook(limit)
}
`,
	})

	var out bytes.Buffer
	err := regenerate(Options{Repo: repo, Manifest: regenFixtureManifest(repo), Out: &out})
	if err != nil {
		t.Fatalf("regenerate: %v\n%s", err, out.String())
	}
	for _, want := range []string{"[write] p/api_low.go", "[write] p/low/export.go"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "[write] p/low/a.go") || strings.Contains(out.String(), "[write] p/b.go") {
		t.Errorf("regen wrote a source file, not just generated ones:\n%s", out.String())
	}

	deps, err := os.ReadFile(filepath.Join(repo, "p/api_low.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deps), "func extra() int { return low.Extra() }") {
		t.Errorf("p/api_low.go lacks the new alias:\n%s", deps)
	}
	exp, err := os.ReadFile(filepath.Join(repo, "p/low/export.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exp), "func Extra() int { return extra() }") {
		t.Errorf("p/low/export.go lacks the new export:\n%s", exp)
	}

	// Nothing was staged or committed: the dirty edits, and the freshly
	// written generated files, are still sitting there as working-tree
	// changes for the caller's own commit to pick up.
	if got := git(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("regen moved HEAD: %s -> %s", head, got)
	}
	st := git(t, repo, "status", "--porcelain")
	for _, want := range []string{"p/low/a.go", "p/b.go", "p/api_low.go", "p/low/export.go"} {
		if !strings.Contains(st, want) {
			t.Errorf("git status lacks %s as a working-tree change:\n%s", want, st)
		}
	}
}

func TestRegen_SecondRunOnAnUnchangedTreeWritesNothing(t *testing.T) {
	repo := regenFixtureRepo(t)
	var out bytes.Buffer
	err := regenerate(Options{Repo: repo, Manifest: regenFixtureManifest(repo), Out: &out})
	if err != nil {
		t.Fatalf("regenerate: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "[write]") || strings.Contains(out.String(), "[remove]") {
		t.Errorf("a clean, already-regenerated tree was rewritten:\n%s", out.String())
	}
	for _, want := range []string{"[skip] p/api_low.go unchanged", "[skip] p/low/export.go unchanged"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if st := git(t, repo, "status", "--porcelain"); st != "" {
		t.Errorf("regen on a clean tree left changes:\n%s", st)
	}
}

func TestRegen_RefusesAHandEditedGeneratedFile(t *testing.T) {
	repo := regenFixtureRepo(t)
	before, err := os.ReadFile(filepath.Join(repo, "p/low/export.go"))
	if err != nil {
		t.Fatal(err)
	}
	handEdited := append(append([]byte{}, before...), []byte("\n// hand edit\n")...)
	writeTree(t, repo, map[string]string{"p/low/export.go": string(handEdited)})

	var out bytes.Buffer
	err = regenerate(Options{Repo: repo, Manifest: regenFixtureManifest(repo), Out: &out})
	if err == nil {
		t.Fatalf("regenerate accepted a hand-edited generated file:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "p/low/export.go") || !strings.Contains(err.Error(), "hand edit") {
		t.Errorf("error does not name the hand-edited file: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(repo, "p/low/export.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, handEdited) {
		t.Errorf("the refused file was overwritten:\n%s", after)
	}
}

func TestRegen_RefusesAFileTheManifestStillNeedsMoved(t *testing.T) {
	repo := regenFixtureRepo(t)
	writeTree(t, repo, map[string]string{
		"tools/tddsplit/manifest.txt": `root p
[packages]
low L0 p/low
p   L1 p
mid L2 p/mid
[files]
a.go      low
a_test.go low
b.go      mid
`,
	})
	var out bytes.Buffer
	err := regenerate(Options{Repo: repo, Manifest: regenFixtureManifest(repo), Out: &out})
	if err == nil {
		t.Fatalf("regenerate accepted a manifest that still needs a move:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "moving") || !strings.Contains(err.Error(), "p/b.go") {
		t.Errorf("error does not name the pending move: %v", err)
	}
	if strings.Contains(out.String(), "[write]") || strings.Contains(out.String(), "[remove]") {
		t.Errorf("a refused run wrote or removed a generated file:\n%s", out.String())
	}
	if st := git(t, repo, "status", "--porcelain"); strings.Contains(st, "p/api_low.go") || strings.Contains(st, "p/low/export.go") {
		t.Errorf("a refused run touched a generated file:\n%s", st)
	}
}

// TestRegen_RemovesAGeneratedFileNoLongerNeeded exercises the stale path: a
// dirty edit drops b.go's only reach into low, so p/api_low.go and
// p/low/export.go stop being needed and must be removed rather than left
// behind carrying the generated-file header for nothing.
func TestRegen_RemovesAGeneratedFileNoLongerNeeded(t *testing.T) {
	repo := regenFixtureRepo(t)
	writeTree(t, repo, map[string]string{
		"p/b.go": "package p\n\nfunc Use() bool { return true }\n",
	})
	var out bytes.Buffer
	err := regenerate(Options{Repo: repo, Manifest: regenFixtureManifest(repo), Out: &out})
	if err != nil {
		t.Fatalf("regenerate: %v\n%s", err, out.String())
	}
	for _, want := range []string{"[remove] p/api_low.go", "[remove] p/low/export.go", "summary:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	for _, p := range []string{"p/api_low.go", "p/low/export.go"} {
		if _, err := os.Stat(filepath.Join(repo, p)); !os.IsNotExist(err) {
			t.Errorf("%s was not removed: %v", p, err)
		}
	}
}
