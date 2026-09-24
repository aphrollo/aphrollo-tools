package merge

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// PrunedLane names one worktree the post-merge sweep removed: its path and
// the branch it held.
type PrunedLane struct {
	Worktree, Branch string
}

// PruneMergedLanesAfterMerge sweeps mainRepo's LINKED worktrees, removing
// every one whose checked-out branch is MERGED into the repo's resolved
// trunk (never hardcoded "main" — `localTrunkBranch` resolves it from
// mainRepo's own checked-out branch, not `trunkBranch`'s remote-tracking
// answer; see localTrunkBranch's own doc for why) — `git for-each-ref
// --merged <trunk>` — with two narrowings:
//
//  1. A branch with no commits of its own never counts as merged, even
//     though `--merged` alone would say so: its tip already sits somewhere
//     on trunk's own first-parent history, either because it was created at
//     trunk's current tip (issue #144: a fresh lane, still clean because a
//     builder had not yet made an edit in it, was pruned out from under them
//     by a sweep that read "clean" as "merged") or because trunk has simply
//     advanced past an older commit the branch never moved beyond (issue
//     #382: the same fresh lane, now merely a few commits behind, passed the
//     #144 check — tip != trunk's CURRENT tip — while still holding no work
//     of its own). Topology is only a PROXY for that, though, and it misses
//     a lane created from a base trunk reaches as a merge's SECOND parent
//     (issue #644), so the same question is also asked DIRECTLY, of the
//     branch's reflog: see zeroCommitLaneKeepReason. The two are independent
//     reasons to keep — a sweep this destructive keeps on either.
//  2. A worktree with uncommitted work — staged, unstaged, or untracked — is
//     never removed regardless of its branch's merge state: the ONE fact
//     that only the live working tree can answer, checked immediately
//     before the removal it gates, never inferred from ref state.
//
// exclude is the worktree running THIS merge (or "" to exclude none) — the
// ground under the process calling this, which must never be swept
// regardless of its own branch's merge state.
//
// A merged branch with a clean tree is, by definition, work already landed
// on trunk with nothing left uncommitted, so there is nothing an --apply
// opt-in would protect that these two checks do not already guarantee;
// every prune is announced on stdout as it happens. A kept lane (rule 2) and
// a removal error are both reported on stderr and do not stop the sweep —
// one worktree a dirty tree, a lock, or a permission refuses is not a reason
// to leave every other landed lane behind too.
func PruneMergedLanesAfterMerge(mainRepo, exclude string, stdout, stderr io.Writer) []PrunedLane {
	if mainRepo == "" {
		return nil
	}
	trunk := localTrunkBranch(mainRepo)
	if trunk == "" {
		return nil
	}
	if gitOut(mainRepo, "rev-parse", trunk) == "" {
		return nil
	}
	merged := mergedBranchTips(mainRepo, trunk)
	mainlineTips := trunkFirstParentTips(mainRepo, trunk)
	mainClean := cleanWorktreePath(mainRepo)
	excludeClean := cleanWorktreePath(exclude)
	var pruned []PrunedLane
	examined := 0
	for _, wt := range mergePruneWorktrees(mainRepo) {
		wtClean := cleanWorktreePath(wt.path)
		if wt.branch == "" || wtClean == mainClean {
			continue // the main clone itself, whatever branch it holds
		}
		if excludeClean != "" && wtClean == excludeClean {
			continue
		}
		examined++
		tip, ok := merged[wt.branch]
		if !ok {
			continue // not merged into trunk at all
		}
		if mainlineTips[tip] {
			continue // no commits of its own (issue #144, generalized by #382)
		}
		if reason := zeroCommitLaneKeepReason(mainRepo, wt.branch); reason != "" {
			fmt.Fprintf(stderr, "prune-lanes: kept %s (%s): %s\n", wt.path, wt.branch, reason)
			continue // issue #644: never carried a commit, or cannot be proven to have
		}
		dirty, err := worktreeHasUncommittedWork(wt.path)
		if err != nil {
			fmt.Fprintf(stderr, "prune-lanes: could not check %s (%s): %v\n", wt.path, wt.branch, err)
			continue
		}
		if dirty {
			fmt.Fprintf(stderr, "prune-lanes: kept %s (%s): uncommitted work\n", wt.path, wt.branch)
			continue
		}
		if err := removeMergedLaneWorktree(mainRepo, wt.path, wt.branch); err != nil {
			fmt.Fprintf(stderr, "prune-lanes: could not prune %s (%s): %v\n", wt.path, wt.branch, err)
			continue
		}
		fmt.Fprintf(stdout, "prune-lanes: pruned %s (%s, merged into %s)\n", wt.path, wt.branch, trunk)
		pruned = append(pruned, PrunedLane{Worktree: wt.path, Branch: wt.branch})
	}
	if len(pruned) == 0 {
		// A sweep that inspected lanes and removed none of them must say so:
		// the silent exit here — issue #710 — is indistinguishable from
		// outside the sweep working correctly with nothing to do, which is
		// what let a repo's trunk resolution go wrong for its whole life
		// unnoticed. One line, never one per lane.
		fmt.Fprintf(stdout, "prune-lanes: examined %d lane(s) against %s; pruned none\n", examined, trunk)
	}
	return pruned
}

// localTrunkBranch answers what the guarded sweep actually needs to know:
// the branch the merge THIS SWEEP is following just landed on. mainRepo is
// always the primary, merge-only checkout (postmerge.go resolves it through
// primaryCheckoutRoot before ever calling this), so that branch is simply
// whatever mainRepo's HEAD is checked out to right now — never a question
// this needs `trunkBranch`'s remote-tracking resolution to answer.
//
// `trunkBranch` prefers refs/remotes/origin/HEAD, which is exactly right for
// its other callers — they are all asking what GitHub, or a `git fetch`,
// would call trunk: the stale-branch push guard (a PR reads against GitHub's
// current base), the pre-merge-PR gate (it builds the same merge GitHub is
// about to make, and fetches origin first), the trunk-merge preview (its own
// doc comment: "GitHub tests the lane against a synthetic merge with the
// CURRENT base branch"), and the ratchet baseline guard's catch-up-merge
// checks (the catch-up they detect is a lane merging the remote-tracked
// trunk into itself). None of those run in a repo whose workflow merges
// straight into a local trunk and pushes only sometimes — issue #710: with
// 837 unpushed commits on local main, `trunkBranch` still answers
// "origin/main", 837 commits behind, and every lane that just landed on
// local main reads as "not merged into trunk at all".
//
// Falls back to trunkBranch when mainRepo's HEAD cannot be read at all
// (detached, or no commit yet) — doubt here means "cannot tell", not "assume
// local", and trunkBranch's own multi-candidate resolution is the better
// guess in that case.
func localTrunkBranch(mainRepo string) string {
	branch, err := git(mainRepo, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		if name := strings.TrimSpace(branch); name != "" && name != "HEAD" {
			return name
		}
	}
	return TrunkBranch(mainRepo)
}

// reflogEntryMarker prefixes every reflog subject zeroCommitLaneKeepReason
// asks git to print. The git helper returns COMBINED output, so a warning on
// stderr would otherwise be indistinguishable from an entry and would read
// as "this branch moved after creation" — the fail-OPEN direction. With the
// marker, a line without it is output this code does not understand, and
// that is a keep.
const reflogEntryMarker = "reflogentry "

// branchCreationReflogSubject is what git writes for `branch`, `checkout -b`
// and `worktree add -b` alike: the one and only entry a branch that has
// never carried a commit of its own has.
const branchCreationReflogSubject = "branch: Created from "

// zeroCommitLaneKeepReason answers the question the sweep actually has —
// "does this branch hold any commit of its own?" — directly, and returns a
// non-empty reason to KEEP the lane, or "" to let the sweep proceed.
//
// Issue #644: trunkFirstParentTips answers a different question, topology,
// as a PROXY for this one, and the proxy fails whenever a lane's creation
// base is not on trunk's first-parent chain — a base reachable from trunk
// only as some merge's SECOND parent. Such a lane is listed by `--merged`
// (its tip really is an ancestor of trunk) and is absent from the mainline
// set, so a brand-new worktree with a clean tree and a builder about to type
// in it scored exactly like a lane whose work had landed, and was deleted.
// `git rev-list --count trunk..branch` cannot rescue it: that count is 0 for
// EVERY branch the merged map holds, since "merged" already means the tip is
// an ancestor — it cannot tell "never had commits" from "had commits that
// landed".
//
// The reflog can. A branch that never carried a commit of its own has
// exactly one reflog entry, its creation; one more entry means the ref moved
// after creation, which is the sweep's rightful catch. This is an
// INDEPENDENT reason to keep, checked after the mainline one and never
// instead of it: a sweep that deletes a builder's directory mid-command
// keeps on EITHER reason, never on both.
//
// Every other shape is doubt, and doubt keeps: an unreadable reflog, one
// that is missing or has been expired away, a single entry that is not a
// creation (a `branch -f` reset, say), or output this code cannot parse.
// The cost of a wrong keep is one stale worktree named on stderr; the cost
// of a wrong delete is a builder's working directory vanishing under them.
func zeroCommitLaneKeepReason(mainRepo, branch string) string {
	out, err := git(mainRepo, "reflog", "show", "--format="+reflogEntryMarker+"%gs", "refs/heads/"+branch)
	if err != nil {
		return fmt.Sprintf("reflog unreadable (%v)", err)
	}
	var entries []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, reflogEntryMarker) {
			return fmt.Sprintf("reflog output not understood (%q)", line)
		}
		entries = append(entries, strings.TrimPrefix(line, reflogEntryMarker))
	}
	switch {
	case len(entries) == 0:
		// git exits 0 with no output for a branch whose reflog is missing
		// or has been expired away: nothing here proves it carried work.
		return "reflog holds no entries — cannot prove it ever carried a commit"
	case len(entries) > 1:
		return "" // the ref moved after creation: it carried commits of its own
	case strings.HasPrefix(entries[0], branchCreationReflogSubject):
		return "no commits of its own (its reflog holds only its creation)"
	default:
		return fmt.Sprintf("reflog shape not understood (%q)", entries[0])
	}
}

// worktreeHasUncommittedWork reports whether path's tree has anything `git
// status` would show — staged, unstaged, or untracked. It is the ONE check
// in this sweep that reads the live working tree rather than ref state, run
// immediately before the removal it gates: a fresh lane with no commits of
// its own, or a real merge commit having landed, are facts about the
// BRANCH, settled the moment this function is called; "clean" is a fact
// about the WORKTREE that can flip between one sweep and the next, so it is
// never cached or inferred.
//
// Issue #382's two pruned worktrees were in fact CLEAN — their builders had
// not yet written a file — which is why the old code's bare `git worktree
// remove`, with no check of its own, still succeeded: git itself already
// refuses a dirty tree. This check is not what would have saved them
// (trunkFirstParentTips is); it turns git's own dirty-tree refusal into an
// explicit, NAMED keep rule — the "kept ... uncommitted work" stderr line —
// instead of a reported removal error, and it is the guard that would
// actually matter once a builder has written a file before the sweep runs.
func worktreeHasUncommittedWork(path string) (bool, error) {
	out, err := git(path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// trunkFirstParentTips is every commit on trunk's OWN mainline — its
// first-parent history — keyed for a tip lookup. A merged branch whose tip
// lands in this set never brought any commit of its own to trunk that is
// not already trunk's own history: either it is the literal issue #144 case
// (a fresh branch created at trunk's current tip), trunk has simply
// advanced past an older commit the branch never moved beyond (issue #382's
// actual bug — see PruneMergedLanesAfterMerge's doc comment), or the branch
// was landed by FAST-FORWARD, which moves trunk's own pointer onto the
// branch's commits, so its tip sits directly on trunk's mainline exactly
// like a never-diverged branch's would. Either way there is no work here
// for THIS sweep to land, so the branch is skipped exactly as #144 already
// did — just no longer keyed to trunk's CURRENT tip alone.
//
// Unlike the other two cases, a fast-forward-landed lane is skipped for
// good, every run, since its tip never stops being a member of this set;
// leaving its worktree behind is always safe (every commit in it already IS
// trunk), and `aphrollo workspace prune` — the separate PR-state-driven
// sweep — still reclaims it once its PR reads MERGED. A lane genuinely
// landed by a real merge commit has a tip that is a SECOND parent of one of
// these mainline commits, never a member of the set itself, so this check
// never catches it.
func trunkFirstParentTips(mainRepo, trunk string) map[string]bool {
	out := gitOut(mainRepo, "rev-list", "--first-parent", trunk)
	tips := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			tips[sha] = true
		}
	}
	return tips
}

// cleanWorktreePath normalizes a worktree path for comparison: forward
// slashes, no trailing slash, case-insensitive — git may report a path with
// either separator, and the same Windows directory routinely appears under
// two drive-letter or short/long-name spellings.
func cleanWorktreePath(p string) string {
	if p == "" {
		return ""
	}
	return strings.ToLower(strings.TrimRight(filepath.ToSlash(filepath.Clean(p)), "/"))
}

// mergedBranchTips is every local branch merged into trunk, mapped to its own
// tip SHA — the SHA is what tells a genuinely-landed branch apart from a
// fresh one sitting at trunk's own tip.
func mergedBranchTips(mainRepo, trunk string) map[string]string {
	out, err := git(mainRepo, "for-each-ref", "--merged", trunk,
		"--format=%(refname:short) %(objectname)", "refs/heads/")
	if err != nil {
		return nil
	}
	tips := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		branch, sha, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || branch == "" {
			continue
		}
		tips[branch] = sha
	}
	return tips
}

// mergePruneWorktree is one linked worktree the sweep considers.
type mergePruneWorktree struct{ path, branch string }

// mergePruneWorktrees lists EVERY worktree `git worktree list` names for
// mainRepo, including the main clone itself — the caller drops that one by
// PATH (cleanWorktreePath(wt.path) == cleanWorktreePath(mainRepo)), never by
// list position or by assuming its branch is named "main": a primary clone
// parked on some other branch must never be swept, and a position-based drop
// trusts an ordering nothing here enforces.
func mergePruneWorktrees(mainRepo string) []mergePruneWorktree {
	out, err := git(mainRepo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var entries []mergePruneWorktree
	var cur mergePruneWorktree
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
		}
		cur = mergePruneWorktree{}
	}
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.path = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			cur.branch = "HEAD"
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			cur.branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	flush()
	return entries
}

// removeMergedLaneWorktree removes a merged lane's worktree and its local
// branch. The successful `worktree remove` has already deleted that lane's
// admin entry; it never follows up with `git worktree prune`, which would also
// delete the entry of every OTHER worktree whose directory this process cannot
// see — a live worktree in the host's /tmp looks missing under systemd
// PrivateTmp.
// It never passes --force to `worktree remove`: the caller already proved
// the tree clean via worktreeHasUncommittedWork immediately before calling
// this, in the same single-threaded sweep, so a plain removal succeeds on
// its own. --force would only paper over something this call has no
// business overriding — a lock, or a tree that somehow turned dirty between
// that check and this one — and a removal error from either is exactly what
// the caller needs to report and skip, not force past.
func removeMergedLaneWorktree(mainRepo, path, branch string) error {
	if _, err := git(mainRepo, "worktree", "remove", path); err != nil {
		return err
	}
	_, err := git(mainRepo, "branch", "-D", "--", branch)
	return err
}
