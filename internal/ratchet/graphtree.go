package ratchet

import (
	"fmt"
	"sync"
)

// GraphTree is where the dep-graph kinds (dep-graph-forbids,
// dep-graph-ceiling, go-dep-graph-forbids) run their graph query. `cargo
// metadata` and `go list` read the manifests and sources off the disk they
// run in, so a run judging anything but the working tree hands them a
// checkout of that tree instead.
type GraphTree struct {
	// Dir is the tree the query runs in.
	Dir string
	// CargoOffline passes --offline to `cargo metadata`: set when the
	// checkout's Cargo.lock is one cargo has already resolved.
	CargoOffline bool
	// CargoTargetDir is CARGO_TARGET_DIR for `cargo metadata`; empty
	// inherits the environment's.
	CargoTargetDir string
	// Overlay is what `go list` reads in place of files on disk; see
	// Options.GraphOverlay.
	Overlay map[string]string
	// atRoot marks the tree as the run's own Root, a path that outlives the
	// run: the only kind a Go graph is cached under, because a checkout made
	// per run would leave one cache file behind for each.
	atRoot bool
}

// graphTreeOf is the tree a Check run's dep-graph laws query, resolved at
// most once and only when a dep-graph law asks for it: Options.GraphTree
// when the caller supplied one, else Root.
func graphTreeOf(opts Options) func() (GraphTree, error) {
	var once sync.Once
	var g GraphTree
	var err error
	return func() (GraphTree, error) {
		once.Do(func() {
			if opts.GraphTree == nil {
				g = GraphTree{Dir: opts.Root, Overlay: goOverlayOf(opts.GraphOverlay), atRoot: true}
				return
			}
			if g, err = opts.GraphTree(); err != nil {
				err = fmt.Errorf("the tree to query the dependency graph in: %w", err)
				return
			}
			g.atRoot = g.Dir == opts.Root
		})
		return g, err
	}
}

// graphLawHits runs one dep-graph law's query over g.
func graphLawHits(g GraphTree, law Law, cacheDir string) ([]Hit, error) {
	law.CacheDir = cacheDir
	law.CargoOffline, law.CargoTargetDir = g.CargoOffline, g.CargoTargetDir
	law.GoOverlay = g.Overlay
	switch law.Matcher.Kind {
	case KindDepGraphForbids:
		return depGraphHits(g.Dir, law)
	case KindDepGraphCeiling:
		return depGraphCeilingHits(g.Dir, law)
	default:
		if !g.atRoot {
			law.CacheDir = ""
		}
		return goDepGraphHits(g.Dir, law)
	}
}

// graphCacheDirOf is where a Check run keeps its dependency-graph cache.
func graphCacheDirOf(opts Options) string {
	if opts.GraphCacheDir != "" {
		return opts.GraphCacheDir
	}
	return opts.CacheDir
}
