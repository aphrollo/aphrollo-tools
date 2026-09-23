package tdd

import (
	"os"
	"path/filepath"
	"strings"
)

// cargoTomlHasWorkspaceTable reports whether manifest declares a top-level
// [workspace] table. Missing/unreadable manifests report false — the caller
// (cargoWorkspaceRoot) then keeps walking up.
func cargoTomlHasWorkspaceTable(manifest string) bool {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		// Tolerate trailing whitespace/comment after the header
		// ("[workspace]  # root"), not just an exact "[workspace]" line --
		// found in review 2026-08-15: a real-world manifest with either
		// silently fell back to "no workspace found here", so a member
		// crate below it ran unscoped from its own directory instead of the
		// resolved workspace root.
		rest, ok := strings.CutPrefix(trimmed, "[workspace]")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "#") {
			return true
		}
	}
	return false
}

// cargoWorkspaceRoot resolves the actual cargo WORKSPACE root for a cargo
// project root: the nearest ancestor directory (root inclusive) whose
// Cargo.toml declares a [workspace] table. Only there does a checked-in
// .config/nextest.toml live, and the workspace's Cargo.lock is there too —
// a member crate's own directory has neither. A crate with no encompassing
// workspace (no [workspace] table found before the filesystem root) falls
// back to being its own "workspace root".
func cargoWorkspaceRoot(root string) string {
	dir := root
	for {
		if cargoTomlHasWorkspaceTable(filepath.Join(dir, "Cargo.toml")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return root // hit the filesystem root without finding one
		}
		dir = parent
	}
}
