package suite

import (
	"os"
	"path/filepath"
	"testing"
)

// Serial: reads the state dir from CLAUDE_CONFIG_DIR, a process-wide env var.
// TestGCStatePath_LivesUnderTheStateDirAndCreatesIt pins the path: the state
// dir is created on demand and the file name joins it.
func TestGCStatePath_LivesUnderTheStateDirAndCreatesIt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	got := gcStatePath("marker.txt")
	if got == "" || filepath.Base(got) != "marker.txt" {
		t.Fatalf("gcStatePath = %q, want a path ending in marker.txt", got)
	}
	if fi, err := os.Stat(filepath.Dir(got)); err != nil || !fi.IsDir() {
		t.Fatalf("the state dir %s was not created: %v", filepath.Dir(got), err)
	}
}

// Serial: reads the state dir from CLAUDE_CONFIG_DIR, a process-wide env var.
// TestGCStatePath_AnUncreatableStateDirHasNoPath pins the failure arm: when the
// state dir cannot be made there is no path, and the caller skips the write.
func TestGCStatePath_AnUncreatableStateDirHasNoPath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", blocker)
	if got := gcStatePath("marker.txt"); got != "" {
		t.Fatalf("gcStatePath = %q, want none when the state dir sits under a file", got)
	}
}
