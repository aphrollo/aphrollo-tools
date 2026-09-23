package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// splitFixture is a package p that builds, vets and tests green both before
// and after a.go (with its test, embedded doc and fuzz corpus) moves to p/low,
// and that another package of the module reaches into.
var splitFixture = map[string]string{
	"go.mod": "module example.com/fx\n\ngo 1.26\n",
	"p/a.go": `package p

import _ "embed"

//go:embed doc.md
var docText string

// Max is read by cmd/x.
const Max = 5

const limit = 3

var hook = func(n int) bool { return n > limit }

type thing struct{ Name string }

func helper(s string) string { return s + docText }
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

func FuzzHelper(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) { _ = helper(s) })
}
`,
	"p/doc.md":                        "doc\n",
	"p/testdata/fuzz/FuzzHelper/seed": "go test fuzz v1\nstring(\"a\")\n",
	"p/b.go":                          "package p\n\n// Use reaches across the split.\nfunc Use() bool {\n\tt := thing{Name: \"x\"}\n\t_ = helper(t.Name)\n\treturn hook(limit)\n}\n",
	"p/b_test.go":                     "package p\n\nimport \"testing\"\n\nfunc TestUse_CallsTheHook(t *testing.T) {\n\tif Use() {\n\t\tt.Fatal(\"3 > 3\")\n\t}\n}\n",
	"cmd/x/main.go":                   "package main\n\nimport \"example.com/fx/p\"\n\nfunc main() { _ = p.Use(); _ = p.Max }\n",
	"tools/tddsplit/manifest.txt": `root p
[packages]
low L0 p/low
p   L1 p
[files]
a.go                      low
a_test.go                 low
doc.md                    low
testdata/fuzz/FuzzHelper/ low
b.go                      p
b_test.go                 p
`,
}

func git(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func fixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	writeTree(t, repo, files)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "fixture")
	return repo
}

func goCmd(t *testing.T, repo string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %v %v: %v\n%s", env, args, err, out)
	}
}

func runFixture(t *testing.T, repo, levels string) (string, error) {
	var out bytes.Buffer
	err := run(Options{Repo: repo, Manifest: filepath.Join(repo, "tools/tddsplit/manifest.txt"), Levels: levels, Out: &out})
	return out.String(), err
}

func TestRun_MovedPackageBuildsVetsAndTestsGreen(t *testing.T) {
	repo := fixtureRepo(t, splitFixture)
	if out, err := runFixture(t, repo, "L0"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, p := range []string{"p/low/a.go", "p/low/a_test.go", "p/low/doc.md", "p/low/testdata/fuzz/FuzzHelper/seed"} {
		if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
			t.Errorf("%s was not moved: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "p/a.go")); !os.IsNotExist(err) {
		t.Errorf("p/a.go still exists after the move")
	}
	moved, err := os.ReadFile(filepath.Join(repo, "p/low/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(splitFixture["p/a.go"], "package p", "package low", 1); string(moved) != want {
		t.Errorf("the moved file changed beyond its package line:\n%s", moved)
	}
	goCmd(t, repo, nil, "vet", "./...")
	goCmd(t, repo, []string{"GOOS=windows"}, "vet", "./...")
	goCmd(t, repo, nil, "test", "-count=1", "./...")
}

func TestRun_SecondRunAfterCommitChangesNothing(t *testing.T) {
	repo := fixtureRepo(t, splitFixture)
	if out, err := runFixture(t, repo, "L0"); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	git(t, repo, "commit", "-q", "-m", "split")
	if out, err := runFixture(t, repo, "L0"); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if st := git(t, repo, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("the second run changed the tree:\n%s", st)
	}
}

func TestRun_RefusesADirtyTree(t *testing.T) {
	repo := fixtureRepo(t, splitFixture)
	writeTree(t, repo, map[string]string{"p/b.go": splitFixture["p/b.go"] + "\n// edited\n"})
	out, err := runFixture(t, repo, "L0")
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("run on a dirty tree: err = %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "p/a.go")); statErr != nil {
		t.Errorf("a refused run still moved a.go: %v", statErr)
	}
}

func TestRun_RefusesAnUnmappedFile(t *testing.T) {
	files := map[string]string{}
	for k, v := range splitFixture {
		files[k] = v
	}
	files["p/c.go"] = "package p\n"
	repo := fixtureRepo(t, files)
	out, err := runFixture(t, repo, "L0")
	if err == nil || !strings.Contains(err.Error(), "c.go") {
		t.Fatalf("run with an unmapped c.go: err = %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "p/a.go")); statErr != nil {
		t.Errorf("a refused run still moved a.go: %v", statErr)
	}
}
