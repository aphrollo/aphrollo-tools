package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// linkedInstall makes dir/frontend/node_modules a real link (a symlink, or a
// directory junction on Windows) to an install elsewhere and returns a file
// inside that install.
func linkedInstall(t *testing.T, dir string) string {
	t.Helper()
	install := filepath.Join(t.TempDir(), "node_modules")
	file := filepath.Join(install, "pkg", "index.js")
	tddtest.MustWrite(t, file, "module.exports = 1\n")
	if err := os.MkdirAll(filepath.Join(dir, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := depinstall.LinkDir(install, filepath.Join(dir, "frontend", "node_modules")); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestRemoveGateWorktree_NeverDeletesThroughALinkedNodeModules proves a
// killed proof's leftover links, which nothing unlinked, cost the install they
// point at nothing when the next run clears the checkout, or removes it
// (#1083).
func TestRemoveGateWorktree_NeverDeletesThroughALinkedNodeModules(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := gateRepo(t)
	wt, err := addGateWorktree(root)
	if err != nil {
		t.Fatalf("addGateWorktree: %v", err)
	}
	installed := linkedInstall(t, wt)

	removeGateWorktree(root, wt)

	if _, err := os.Stat(installed); err != nil {
		t.Errorf("removing the checkout deleted through the link into its target: %v", err)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("the checkout is still on disk after removal (%v)", err)
	}
}

func TestAddGateWorktree_ClearingALeftoverNeverDeletesThroughALinkedNodeModules(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := gateRepo(t)
	first, err := addGateWorktree(root)
	if err != nil {
		t.Fatalf("addGateWorktree: %v", err)
	}
	installed := linkedInstall(t, first)

	second, err := addGateWorktree(root)
	if err != nil {
		t.Fatalf("addGateWorktree over a leftover: %v", err)
	}
	defer removeGateWorktree(root, second)

	if _, err := os.Stat(installed); err != nil {
		t.Errorf("clearing the leftover deleted through the link into its target: %v", err)
	}
}
