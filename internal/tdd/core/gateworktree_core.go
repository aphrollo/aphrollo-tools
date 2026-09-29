package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The gate's own checkout of a repo: one stable worktree per repo under the
// state dir, created at HEAD for the length of one stage and removed after
// it. Fail-first applies the staged tests to it and then the whole staged
// tree; the commit ratchet stage writes the staged tree into it for the
// dep-graph laws, whose graph query reads the disk it runs in.

// gcOriginFile records, beside a hash-named gate directory, which repo root
// it belongs to -- the only way to tell a live gate dir from the remains of
// a repo that was deleted months ago.
const gcOriginFile = "origin.txt"

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

// gateWorktreeDir returns the stable per-repo path for the gate worktree,
// under the state dir. Stability is the point: cargo fingerprints bake in
// absolute source paths, so a fresh MkdirTemp per commit cold-rebuilds the
// workspace crates every time even with a warm CARGO_TARGET_DIR. "" when
// there is no state dir (the caller then falls back to a temp dir).
func gateWorktreeDir(repoRoot string) string {
	base := StateDir()
	if base == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(repoRoot))
	dir := filepath.Join(base, "failfirst-wt", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return ""
	}
	writeGateOrigin(dir, repoRoot)
	return dir
}

// addGateWorktree checks HEAD out into the gate worktree, clearing a leftover
// registration a crashed run left at the same stable path first. The caller
// removes it with removeGateWorktree.
func addGateWorktree(repoRoot string) (string, error) {
	wt := gateWorktreeDir(repoRoot)
	if wt == "" {
		var err error
		if wt, err = os.MkdirTemp("", "gate-failfirst-"); err != nil {
			return "", err
		}
	} else {
		_, _ = git(repoRoot, "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(wt)
	}
	if _, err := git(repoRoot, "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		_ = os.RemoveAll(wt)
		return "", err
	}
	return wt, nil
}

// removeGateWorktree unregisters the gate worktree and deletes what is left
// of it. Best-effort: a registration that outlives it is cleared by the next
// addGateWorktree.
func removeGateWorktree(repoRoot, wt string) {
	_, _ = git(repoRoot, "worktree", "remove", "--force", wt)
	_ = os.RemoveAll(wt)
}

// readStagedTree replaces the gate worktree wt's files with repoRoot's
// staged tree: --reset takes the staged content over whatever wt carries,
// adds the staged new files and drops the staged deletions. The error
// carries git's own words.
func readStagedTree(repoRoot, wt string) (string, error) {
	// An empty wt would run read-tree in the process's own directory and
	// replace whatever checkout that is.
	if wt == "" {
		return "", errors.New("no gate worktree to read the staged tree into")
	}
	// A write-tree failure leaves no tree id, and read-tree below refuses
	// what it printed instead.
	tree, _ := git(repoRoot, "write-tree")
	out, err := git(wt, "read-tree", "-u", "--reset", strings.TrimSpace(tree))
	return strings.TrimSpace(out), err
}
