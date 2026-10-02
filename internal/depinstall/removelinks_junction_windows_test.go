//go:build windows

package depinstall

import (
	"os"
	"path/filepath"
	"testing"
)

// A real directory junction, made the way a lane's frontend/node_modules is
// (mklink /J), not the stand-in the seam provides: removing the lane must
// unlink it and leave the target's contents whole (#1083).
func TestRemoveTree_ARealJunctionIsUnlinkedAndItsTargetKept(t *testing.T) {
	nm, file := sharedInstall(t)
	lane := filepath.Join(t.TempDir(), "lane")
	link := filepath.Join(lane, "frontend", NodeModules)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := makeJunction(nm, link); err != nil {
		t.Fatalf("no junction on this box: %v", err)
	}
	if _, err := os.Stat(filepath.Join(link, "fakepkg", "index.js")); err != nil {
		t.Fatalf("the junction does not reach its target: %v", err)
	}

	if err := RemoveTree(lane); err != nil {
		t.Fatalf("RemoveTree: %v", err)
	}

	if _, err := os.Lstat(lane); !os.IsNotExist(err) {
		t.Errorf("the lane must be gone, lstat err = %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the junction's target lost its contents: %v", err)
	}
}
