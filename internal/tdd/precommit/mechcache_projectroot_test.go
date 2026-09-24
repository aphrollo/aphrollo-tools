package precommit

import (
	"fmt"
	"path/filepath"
	"testing"
)

// The mechanical green cache keyed on (repo, worktree state, command). Both
// the state hash and the command are the same for two project roots of one
// repo that run the same relative command: the state hash is computed over
// the repo-wide diff, and `go test -race -count=1 -shuffle=on ./pkg` in two
// Go modules is one string. So module a's green was recorded under a key that
// module b's run then hit, and the merge gate allowed a merge whose b suite
// was red, reporting it "cache-hit".

// twoModuleMerge is a repo holding two Go modules, a and b, each with one
// package of the same name and a test. The staged change moves both, and
// leaves b's test red.
func twoModuleMerge(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	module := func(dir string, x, want int) {
		write(t, root, dir+"/go.mod", "module "+dir+"\n\ngo 1.21\n")
		write(t, root, dir+"/pkg/x.go", fmt.Sprintf("package pkg\n\nfunc X() int { return %d }\n", x))
		write(t, root, dir+"/pkg/x_test.go", fmt.Sprintf("package pkg\n\nimport \"testing\"\n\n"+
			"func TestX(t *testing.T) {\n\tif X() != %d {\n\t\tt.Fatal(X())\n\t}\n}\n", want))
	}
	module("a", 1, 1)
	module("b", 1, 1)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	module("a", 2, 2)
	module("b", 2, 3)
	gitDo(t, root, "add", ".")
	return root
}

func TestMechanical_AGreenInOneProjectRootIsNoCacheHitForAnother(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := twoModuleMerge(t)

	res := Mechanical(root, RunSuite(precommitTestTimeout))

	if !res.Blocked {
		t.Fatalf("module b's suite is red, yet the merge gate allowed the merge — a's green satisfied b's lookup; got %+v", res)
	}
}

// The key-level statement of the same fact, and its other half: two
// worktrees of one repo still share a key for the same project root.
func TestMechKey_DistinctPerProjectRootSharedAcrossWorktrees(t *testing.T) {
	root := twoModuleMerge(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, root, "worktree", "add", "-q", "-b", "lane/x", lane)
	r := Runner{Cmd: "go", Args: []string{"test", "./pkg"}}

	if mechKey(filepath.Join(root, "a"), "h", r) == mechKey(filepath.Join(root, "b"), "h", r) {
		t.Error("two project roots of one repo running one command must never share a cache key")
	}
	if mechKey(filepath.Join(root, "a"), "h", r) != mechKey(filepath.Join(lane, "a"), "h", r) {
		t.Error("the same project root in two worktrees of one repo must share its cache key")
	}
}
