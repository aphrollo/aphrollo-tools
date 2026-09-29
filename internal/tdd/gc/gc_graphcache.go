package gc

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// Category (m): a per-worktree Go dependency-graph cache whose worktree is
// gone. The ratchet's graph laws cache `go list`'s answer per checkout in
// <state>/ratchet-cache/golist-<name>-<hash>.json (about 730 KB each), and
// nothing else ever removes the cache of a lane that was pruned. The file's
// header records the checkout it was made for, so the sweep asks the
// filesystem about that path instead of guessing from a hash.
//
// Only a PROVEN-missing checkout qualifies, the rule gcStaleGateDirs applies
// to a gate dir: any other Stat error is "could not look", and a cache with
// no recorded root is unknown and left alone.
// Candidates come in filepath.Glob's lexical order.
func gcGraphCaches(stateDir string) []GCCandidate {
	if stateDir == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(stateDir, "ratchet-cache", ratchet.GraphCachePrefix+"*.json"))
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, path := range files {
		root, ok := ratchet.GraphCacheRoot(path)
		if !ok {
			continue
		}
		if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var size int64
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}
		out = append(out, GCCandidate{
			Path:   path,
			Size:   size,
			Reason: "Go dependency-graph cache of " + root + ", which no longer exists",
			Kind:   GCKindTempLitter,
		})
	}
	return out
}
