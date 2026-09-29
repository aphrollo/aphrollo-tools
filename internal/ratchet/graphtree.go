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
				g = GraphTree{Dir: opts.Root}
				return
			}
			if g, err = opts.GraphTree(); err != nil {
				err = fmt.Errorf("the tree to query the dependency graph in: %w", err)
			}
		})
		return g, err
	}
}

// graphLawHits runs one dep-graph law's query over g.
func graphLawHits(g GraphTree, law Law, cacheDir string) ([]Hit, error) {
	law.CacheDir = cacheDir
	law.CargoOffline, law.CargoTargetDir = g.CargoOffline, g.CargoTargetDir
	switch law.Matcher.Kind {
	case KindDepGraphForbids:
		return depGraphHits(g.Dir, law)
	case KindDepGraphCeiling:
		return depGraphCeilingHits(g.Dir, law)
	default:
		return goDepGraphHits(g.Dir, law)
	}
}
