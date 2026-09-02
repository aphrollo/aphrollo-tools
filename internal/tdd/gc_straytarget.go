package tdd

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Category (h): a cargo target dir that is not THE target dir. They appear by
// hand — a `target-sky/` built once with a different CARGO_TARGET_DIR, 33 GB
// of it found idle on one box — and nothing ever collects them, because every
// other category is about a directory this binary itself created. Two files
// cargo always writes identify one beyond doubt, and being idle for days is
// what says nobody is building into it.
const (
	cargoCacheTag  = "CACHEDIR.TAG"
	cargoInfoFile  = ".rustc_info.json"
	strayTargetTop = 1 // depth under a root: deeper is somebody's own layout
)

// gcStrayTargetDirs proposes every directory at depth 1 under one of roots
// that carries cargo's two marker files, is not the resolved target dir, and
// whose newest file is older than olderThan.
func gcStrayTargetDirs(roots []string, resolved string, olderThan time.Duration, now time.Time) []GCCandidate {
	live := pathKey(resolved)
	seen := map[string]bool{}
	var out []GCCandidate
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(root, e.Name())
			key := pathKey(path)
			if key == live || seen[key] || !isCargoTargetDir(path) {
				continue
			}
			seen[key] = true
			newest, size := dirNewestAndSize(path)
			idle := now.Sub(newest)
			if newest.IsZero() || idle < olderThan {
				continue
			}
			out = append(out, GCCandidate{
				Path:   path,
				Size:   size,
				Reason: "stray cargo target dir (not the resolved target), idle " + formatDays(idle),
				Kind:   GCKindStrayTarget,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// isCargoTargetDir reports whether dir carries BOTH files cargo writes at the
// top of a target dir. The tag alone is not enough — plenty of caches carry
// one — and requiring both is what keeps this from proposing somebody's data.
func isCargoTargetDir(dir string) bool {
	for _, name := range []string{cargoCacheTag, cargoInfoFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// strayTargetRoots is where a stray can appear: the repo root and every
// registered worktree root, each scanned one level deep.
func strayTargetRoots(repo string) []string {
	roots := []string{repo}
	root := RepoRoot(repo)
	if root == "" {
		return roots
	}
	if pathKey(root) != pathKey(repo) {
		roots = append(roots, root)
	}
	for _, path := range gitWorktreePaths(root) {
		if pathKey(path) != pathKey(root) && pathKey(path) != pathKey(repo) {
			roots = append(roots, path)
		}
	}
	return roots
}
