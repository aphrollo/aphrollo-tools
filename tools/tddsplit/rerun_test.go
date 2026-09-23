package main

import (
	"os"
	"path/filepath"
	"testing"
)

// layeredFixture is a package p carved in two steps: a.go to p/low (L0),
// then m.go and its test to p/mid (L1). Every package with tests owns a
// TestMain, as internal/tdd's guard test demands, so the root's
// main_test.go and each carved package's own one sit side by side once the
// first step lands.
var layeredFixture = map[string]string{
	"go.mod":         "module example.com/fx\n\ngo 1.26\n",
	"p/a.go":         "package p\n\nfunc base() int { return 1 }\n",
	"p/m.go":         "package p\n\nfunc mid() int { return base() + 1 }\n",
	"p/m_test.go":    "package p\n\nimport \"testing\"\n\nfunc TestMid_AddsOne(t *testing.T) {\n\tif mid() != 2 {\n\t\tt.Fatal(mid())\n\t}\n}\n",
	"p/b.go":         "package p\n\n// Top reaches both lower levels.\nfunc Top() int { return mid() }\n",
	"p/main_test.go": "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n",
	"tools/tddsplit/manifest.txt": `root p
[packages]
low L0 p/low
mid L1 p/mid
p   L2 p
[files]
a.go         low
m.go         mid
m_test.go    mid
b.go         p
main_test.go p
`,
}

// A carved package gets its own TestMain beside the root's. The manifest
// declares it [local]: the generator leaves it where it is and out of the
// reassembled package, so re-running a level already carved changes
// nothing, and the next level carves on top of it.
func TestRun_ReRunsAndCarvesOnTopOfAPartlySplitTree(t *testing.T) {
	repo := fixtureRepo(t, layeredFixture)
	if out, err := runFixture(t, repo, "L0"); err != nil {
		t.Fatalf("carving L0: %v\n%s", err, out)
	}
	git(t, repo, "commit", "-q", "-m", "carve L0")
	writeTree(t, repo, map[string]string{
		"p/low/main_test.go":          "package low\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n",
		"tools/tddsplit/manifest.txt": layeredFixture["tools/tddsplit/manifest.txt"] + "[local]\nlow/main_test.go\n",
	})
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "low's own TestMain")

	if out, err := runFixture(t, repo, "L0"); err != nil {
		t.Fatalf("re-running L0 on the carved tree: %v\n%s", err, out)
	}
	if st := git(t, repo, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("re-running L0 changed the tree:\n%s", st)
	}
	if out, err := runFixture(t, repo, "L1"); err != nil {
		t.Fatalf("carving L1 on top of L0: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(repo, "p/mid/m.go")); err != nil {
		t.Errorf("m.go was not carved into p/mid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "p/low/main_test.go")); err != nil {
		t.Errorf("low's own TestMain did not stay put: %v", err)
	}
	goCmd(t, repo, nil, "vet", "./...")
	goCmd(t, repo, nil, "test", "-count=1", "./...")
}
