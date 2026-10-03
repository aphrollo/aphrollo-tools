package git

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Markers a path's blob field holds when it has no file content to hash.
const (
	blobGone    = "-"       // the path is not in the worktree
	blobDir     = "dir"     // a directory: a nested repository git lists as one path
	blobSpecial = "special" // a device, pipe or socket
)

// WorktreeKey names the worktree as it stands, for the verdicts that measured
// it (architecture §3 "Tree keys"): the sha256 of HEAD's tree oid and the
// sorted (path, blob) pair of every path that is modified, staged or
// untracked. A verdict names the key it measured, and a commit maps its
// worktree key to its tree oid so CI's verdict can be joined to it.
//
// The blob is the object name git would give the file as it lies in the
// worktree, hashed here from its bytes (a symlink by its target text) in the
// repository's own object format, so the key costs two spawns (HEAD's tree and
// the status) and never depends on a clean filter or on how many paths changed.
// A path that is gone, a directory and a submodule carry a marker in place of a
// blob (a submodule, its status field). The key names content: staging a change
// does not move it (the index tree is its own key), a path's mode bits are not
// in it, and an ignored file is not part of the tree. A rename is the source
// gone and the destination present.
//
// A repository with no commit keys its untracked files against an empty tree.
// A tree that cannot be read is an error, never a key of what could be.
func (c *Client) WorktreeKey() (string, error) {
	st, err := c.Status("")
	if err != nil {
		return "", fmt.Errorf("git: worktree key: %w", err)
	}
	tree := ""
	if !st.Branch.Initial {
		out, err := c.Output("rev-parse", "--verify", "HEAD^{tree}")
		if err != nil {
			return "", fmt.Errorf("git: worktree key: HEAD's tree: %w", err)
		}
		tree = strings.TrimSpace(out)
	}
	newHash := sha1.New
	if len(tree) == sha256.Size*2 {
		newHash = sha256.New
	}

	blobs := map[string]string{}
	for _, e := range st.Entries {
		switch {
		case e.Kind == Ignored:
		case e.IsSubmodule():
			blobs[e.Path] = "sub:" + e.XY + e.Sub
		default:
			if e.Kind == Renamed {
				blobs[e.From] = blobGone
			}
			blobs[e.Path] = worktreeBlob(filepath.Join(c.root, filepath.FromSlash(e.Path)), newHash)
		}
	}
	paths := make([]string, 0, len(blobs))
	for p := range blobs {
		paths = append(paths, p)
	}
	slices.Sort(paths)

	sum := sha256.New()
	fmt.Fprintf(sum, "tree %s\n", tree)
	for _, p := range paths {
		// A path holds no NUL and a blob no NUL or newline: the record cannot be read two ways.
		fmt.Fprintf(sum, "%s\x00%s\n", p, blobs[p])
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// worktreeBlob is the object name of the file at path as it lies on disk, or a
// marker when there is no file content to name.
func worktreeBlob(path string, newHash func() hash.Hash) string {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return blobGone
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return blobGone
		}
		return blobOf(newHash(), int64(len(target)), strings.NewReader(target))
	case info.IsDir():
		return blobDir
	case !info.Mode().IsRegular():
		return blobSpecial
	}
	f, err := os.Open(path)
	if err != nil {
		return blobGone
	}
	defer f.Close()
	return blobOf(newHash(), info.Size(), f)
}

// blobOf hashes size bytes of r the way git names a blob: over "blob <size>\x00" and the content.
func blobOf(h hash.Hash, size int64, r io.Reader) string {
	_, _ = io.WriteString(h, "blob "+strconv.FormatInt(size, 10)+"\x00")
	if _, err := io.Copy(h, r); err != nil {
		return blobGone
	}
	return hex.EncodeToString(h.Sum(nil))
}
