package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corruptIndex makes `git diff --cached` fail the way a hook meets it in the
// wild — an index another process is mid-write on, a stale GIT_DIR, a shim
// that failed to resolve. Truncating the index file is the deterministic
// stand-in: git exits 128 with "index file smaller than expected".
func corruptIndex(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".git", "index"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "diff", "--cached", "--name-only"); err == nil {
		t.Fatal("setup: git still reads the index after corrupting it")
	}
}

// A gate that cannot read the index does not know whether anything is staged,
// so "nothing is staged" is not a verdict it is entitled to reach. It must
// refuse and name the git failure — otherwise an index.lock collision at hook
// time lands a commit with every per-root stage silently skipped.
func TestPrecommit_RefusesWhenIndexUnreadable(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	corruptIndex(t, root)

	res := precommitDecide(root, func(r Runner, dir string) SuiteResult {
		t.Fatalf("no suite may run when the index is unreadable: %+v", r)
		return SuiteResult{}
	})
	if !res.Blocked {
		t.Fatalf("an unreadable index must BLOCK the commit, got %+v", res)
	}
	if !strings.Contains(res.Message, "index file") {
		t.Fatalf("the refusal must quote the git error that caused it; got %q", res.Message)
	}
}

// The merge gate reads the same index through the same helper, and a merge
// waved through unmeasured is the more expensive of the two mistakes.
func TestMechanical_RefusesWhenIndexUnreadable(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	corruptIndex(t, root)

	res := Mechanical(root, func(r Runner, dir string) SuiteResult {
		t.Fatalf("no suite may run when the index is unreadable: %+v", r)
		return SuiteResult{}
	})
	if !res.Blocked {
		t.Fatalf("an unreadable index must BLOCK the merge, got %+v", res)
	}
	if !strings.Contains(res.Message, "index file") {
		t.Fatalf("the refusal must quote the git error that caused it; got %q", res.Message)
	}
}
