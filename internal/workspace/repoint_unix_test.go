//go:build !windows

package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// repointSymlink's rename is the step that actually swaps the link, so its
// error is the one a caller must see: claim and unclaim both treat a nil
// return as "the link now points where I asked". Swallowing it reports a
// repoint that never happened.
//
// A non-empty DIRECTORY at the link path makes rename(2) fail (it refuses to
// replace a directory with a non-directory), which is a real failure of the
// real call rather than a stubbed one.
func TestRepointSymlink_ReturnsTheErrorWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(dir, "link")
	if err := os.Mkdir(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := repointSymlink(occupied, target); err == nil {
		t.Fatal("repointSymlink returned nil for a rename that cannot succeed — a caller would record a repoint that never happened")
	}
	// The failed attempt must not leave its temp link behind.
	if _, err := os.Lstat(occupied + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp link %q survived a failed repoint", occupied+".tmp")
	}
}
