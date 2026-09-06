package a

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadsAFixtureFromATempDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestLocatesItsOwnFileNotTheSourceTree(t *testing.T) {
	// tree-read-ok: locates THIS test's own file to read a sibling in the
	// same package, never a path outside it — fixture text for test_tree_read.
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	_ = self
}
