package gc

import (
	"path/filepath"
	"sort"
	"testing"
)

// TestApplyGCFor_RefusedOrderIsSortedAcrossMultipleTargetInterlocks pins
// #180: a sweep whose candidates span more than one target-dir interlock
// (the repo's own target dir plus a separate orphan worktree's own
// <path>/target is the normal case once more than one stale worktree
// accumulates) bucketed candidates into a map keyed by target dir and
// appended each bucket's refusals in MAP ITERATION order — randomized by Go
// on every range, not just every process — so `refused`, and therefore the
// `aphrollo gate gc --apply` stderr line order, moved run to run on
// identical input. The Deterministic contract (CLAUDE.md: "same inputs,
// same bytes out (sorted, stable)") requires refused to come out sorted
// regardless of which target's build slot happened to be acquired first.
func TestApplyGCFor_RefusedOrderIsSortedAcrossMultipleTargetInterlocks(t *testing.T) {
	withIsolatedBuildLock(t)
	repo := t.TempDir()

	// Both candidates are refused outright (a "deps" path component is
	// protected regardless of Kind unless the kind names its own artifacts,
	// which GCKindOrphanWorktree does not) — no filesystem interaction is
	// needed to produce a refusal, only two DISTINCT target interlocks.
	one := filepath.Join(t.TempDir(), "lane-one", "deps")
	two := filepath.Join(t.TempDir(), "lane-two", "deps")
	cands := []GCCandidate{
		{Path: one, Size: 1, Reason: "orphan build dir", Kind: GCKindOrphanWorktree},
		{Path: two, Size: 1, Reason: "orphan build dir", Kind: GCKindOrphanWorktree},
	}
	want := []string{one, two}
	sort.Strings(want)

	for i := 0; i < 50; i++ {
		_, refused, _ := ApplyGCFor(repo, cands)
		if len(refused) != 2 {
			t.Fatalf("run %d: refused = %v, want both candidates refused", i, refused)
		}
		if refused[0] != want[0] || refused[1] != want[1] {
			t.Fatalf("run %d: refused = %v, want sorted order %v", i, refused, want)
		}
	}
}
