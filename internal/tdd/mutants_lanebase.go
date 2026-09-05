package tdd

import "strings"

// A lane's mutation run measures the diff from its base to HEAD, and the base
// has to be the newest trunk commit the lane ALREADY CONTAINS. Anything
// already merged in is, by definition, not what the lane is proposing: it
// passed its own gate and its own mutation run on trunk before it got there.
//
// laneBaseRef alone cannot answer that. It prefers `origin/main`, and nothing
// in this package fetches, so that remote-tracking ref is whatever it last
// happened to be. A lane that catches up by merging its LOCAL main then
// resolves a base from before the merge, and the run charges it for every
// change trunk made in between — one real run measured 40 files and two
// crates the lane never opened (issue #261).
//
// The gate now DRIVES lanes into that state: the push guard refuses a stale
// branch and prints a catch-up merge as the remedy, so a base that mishandles
// the catch-up is on the common path rather than the rare one.

// laneBaseCandidates are the refs a base may be taken against, most
// authoritative first. The local branch is here as well as the remote-tracking
// one precisely because it is the one a catch-up merge advances.
var laneBaseCandidates = []string{"origin/main", "origin/master", "main", "master"}

// laneBaseSHA resolves the commit a lane's own diff is taken from: the
// merge-base against each trunk candidate that exists, keeping whichever is a
// DESCENDANT of the others. No fetch is needed — a commit the lane already
// contains is already local, which is the whole reason this works offline.
func laneBaseSHA(root string) string {
	best := ""
	for _, ref := range laneBaseCandidates {
		if _, err := git(root, "rev-parse", "--verify", "--quiet", ref); err != nil {
			continue
		}
		base := strings.TrimSpace(gitOut(root, "merge-base", ref, "HEAD"))
		if base == "" {
			continue
		}
		if best == "" || isAncestorCommit(root, best, base) {
			best = base
		}
	}
	if best == "" {
		// No trunk to compare against at all: the previous commit is the only
		// honest answer, and it is what laneBaseRef already falls back to.
		best = strings.TrimSpace(gitOut(root, "merge-base", "HEAD~1", "HEAD"))
	}
	return best
}

// isAncestorCommit reports whether ancestor is reachable from descendant —
// the test for "this base is further along than that one". A commit is not
// treated as its own ancestor here, so an unchanged best is never replaced.
func isAncestorCommit(root, ancestor, descendant string) bool {
	if ancestor == descendant {
		return false
	}
	_, err := git(root, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}
