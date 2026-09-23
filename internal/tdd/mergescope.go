package tdd

import "strings"

// The staged set a gate judges is the index against a base commit, and for an
// ordinary commit or a lane landing on trunk that base is HEAD: everything the
// commit or the merge brings in is what must be judged. A merge of trunk INTO
// a lane is the one shape where HEAD is the wrong base (issue #726). HEAD is
// the lane tip there, so the index-against-HEAD diff is everything trunk
// gained since the lane forked -- work that was already judged when it landed
// on trunk -- and a docs-only lane paid a full build plus suites for crates it
// never touched.
//
// For that shape the base is the incoming trunk tip instead. The index
// against it is exactly what this lane would still add to trunk: its own
// changes since the fork, plus any conflict resolution the merge wrote, and
// nothing trunk already carries. It is the same set the later lane-into-trunk
// merge will judge, so nothing the lane owes goes unjudged.

// stagedDiffBase names the commit the staged set is diffed against, and ""
// for git's own default (HEAD, or the empty tree before the first commit).
func stagedDiffBase(repoRoot string) string {
	if tip, ok := trunkSyncTip(repoRoot); ok {
		return tip
	}
	return ""
}

// stagedBaseRev is stagedDiffBase for a caller that reads a blob at the base:
// HEAD whenever the merge in progress is not a trunk sync.
func stagedBaseRev(repoRoot string) string {
	if base := stagedDiffBase(repoRoot); base != "" {
		return base
	}
	return "HEAD"
}

// trunkSyncTip reports the incoming tip when the merge in progress brings
// trunk into a lane: HEAD is a named branch that is not trunk, and the tip
// coming in is already contained in trunk. Both halves are required, and each
// failure keeps today's base:
//
//   - HEAD on trunk (the primary taking a lane, or GatePRMerge's detached
//     checkout at trunk's tip) is the merge that lands work and must judge
//     all of it.
//   - An incoming tip trunk does not contain (one lane merged into another)
//     brings work no gate has judged yet.
//
// "Contained in trunk" is checked against the resolved trunk ref and its
// local branch: on this box a lane lands on the primary's local trunk before
// any push, so `git merge main` in a lane names a commit origin may not have.
func trunkSyncTip(repoRoot string) (string, bool) {
	tip, ok := mergeTipOf(repoRoot)
	if !ok {
		return "", false
	}
	trunk := TrunkBranch(repoRoot)
	branch := gitOut(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if trunk == "" || branch == "" || branch == "HEAD" || branchIsTrunk(branch, trunk) {
		return "", false
	}
	for _, ref := range trunkRefs(trunk) {
		if _, err := git(repoRoot, "merge-base", "--is-ancestor", tip.Rev, ref); err == nil {
			return tip.Rev, true
		}
	}
	return "", false
}

// trunkRefs is trunk as resolved, then its local branch when the resolved
// name is the remote-tracking one.
func trunkRefs(trunk string) []string {
	refs := []string{trunk}
	if local := strings.TrimPrefix(trunk, "origin/"); local != trunk {
		refs = append(refs, local)
	}
	return refs
}
