package suite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGoTmpDir_HasNoGitAncestor pins the property that actually matters,
// not the literal path: issue #532 traced every failure to goTmpDir landing
// INSIDE the worktree it scratches for, so `.git` (one of rootMarkers) was
// an ancestor of every t.TempDir() fixture a `go test` child created there.
// Walking a real checkout's resolved scratch dir up to the filesystem root
// must never cross a `.git` file or directory.
func TestGoTmpDir_HasNoGitAncestor(t *testing.T) {
	root := makeGoRepo(t)
	tmp := goTmpDir(root)
	if tmp == "" {
		t.Fatalf("goTmpDir(%q) = \"\", want a resolved path for a real git checkout", root)
	}
	for dir := tmp; ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			t.Fatalf("goTmpDir(%q) = %q has a .git ancestor at %q", root, tmp, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

// TestFindProjectRoot_MarkerlessTreeUnderGoTmpDirStaysRootless is the
// regression itself (issue #532): PR #528 pointed GOTMPDIR/TMPDIR/TMP/TEMP
// at a directory inside the worktree, so a marker-less fixture tree any
// `go test` child built under it (via t.TempDir(), which reads TMPDIR/TMP/
// TEMP) had the worktree's own `.git` as an ancestor, and FindProjectRoot
// returned a root where the test asked for none — the exact failure
// reported as "runner_test.go:37: expected no root for a marker-less tree,
// got <repo>". A marker-less tree built under the resolved go scratch dir
// must stay rootless.
func TestFindProjectRoot_MarkerlessTreeUnderGoTmpDirStaysRootless(t *testing.T) {
	root := makeGoRepo(t)
	tmp := goTmpDir(root)
	if tmp == "" {
		t.Fatalf("goTmpDir(%q) = \"\", want a resolved path for a real git checkout", root)
	}

	sub := filepath.Join(tmp, "TestSomething", "001")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "loose.go")
	if err := os.WriteFile(file, []byte("package m\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := FindProjectRoot(file); got != "" {
		t.Fatalf("FindProjectRoot(%q) = %q, want \"\" — a marker-less tree under the go scratch dir must stay rootless (issue #532)", file, got)
	}
}
