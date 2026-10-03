package suite

import (
	"strings"
)

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

// laneBaseCandidates are the refs a base may be taken against: the repo's
// resolved trunk (TrunkBranch, the one trunk resolution every gate shares) and,
// when that is the remote-tracking name, its local branch as well. The local
// branch is here precisely because it is the one a catch-up merge advances.
// Names the repo merely happens to carry (a stale `master` beside a trunk of
// another name) are not trunk and are never asked about. None resolved gives
// none, which sends laneBaseSHA to its documented fallback.
func laneBaseCandidates(root string) []string {
	trunk := TrunkBranch(root)
	if trunk == "" {
		return nil
	}
	refs := []string{trunk}
	if local := strings.TrimPrefix(trunk, "origin/"); local != trunk {
		refs = append(refs, local)
	}
	return refs
}

// laneBaseSHA resolves the commit a lane's own diff is taken from: the
// merge-base against each trunk candidate that exists (see laneBaseCandidates), keeping whichever is a
// DESCENDANT of the others. No fetch is needed — a commit the lane already
// contains is already local, which is the whole reason this works offline.
//
// It is the ONE base resolution in this package: the mutation run, the
// baseline guard's comparison ref and the lane's changed-path set all take it
// from here, so no caller can quietly revert to "whichever ref resolved first".
func laneBaseSHA(root string) string {
	best := ""
	for _, ref := range laneBaseCandidates(root) {
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
		// honest answer. A repo with a single commit has not even that, and ""
		// sends the baseline guard to its documented HEAD fallback.
		best = strings.TrimSpace(gitOut(root, "merge-base", "HEAD~1", "HEAD"))
	}
	return best
}
