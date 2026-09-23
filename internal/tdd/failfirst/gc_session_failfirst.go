package failfirst

import (
	"os"
	"path/filepath"
)

// writeGateOrigin records which repo a hash-named gate directory belongs to.
// Written at creation and never read by the gate itself: it exists so the
// sweep can tell a live directory from the remains of a deleted repo, which
// the hash alone can never say. Best-effort — a missing origin only makes
// the directory UNKNOWN, and unknown directories are left alone.
func writeGateOrigin(dir, root string) {
	if dir == "" || root == "" {
		return
	}
	path := filepath.Join(dir, gcOriginFile)
	if data, err := os.ReadFile(path); err == nil && string(data) == root {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(root), 0o600)
}
