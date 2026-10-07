package cli

import (
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// A user-space binary's queue shims live beside the root, not in a version
// directory the next update leaves behind on PATH.
func TestDefaultCargoShimDir_AUserSpaceBinGetsTheStableShimDir(t *testing.T) {
	userspaceHome(t)
	root, err := userbin.Root()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(root), "cargo-queue")
	if got := defaultCargoShimDir(userbin.BinaryPath(root, "7.1.0")); got != want {
		t.Errorf("shim dir = %q, want %q", got, want)
	}
}
