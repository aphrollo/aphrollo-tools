package tdd

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
// trunk (never hardcoded "main" — `trunkBranch` is the same resolution every
// law in this package uses) — `git branch --merged <trunk>` — with two
// narrowings:
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
//     of its own).
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
	trunk := trunkBranch(mainRepo)
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
	for _, wt := range mergePruneWorktrees(mainRepo) {
		wtClean := cleanWorktreePath(wt.path)
		if wt.branch == "" || wtClean == mainClean {
			continue // the main clone itself, whatever branch it holds
		}
		if excludeClean != "" && wtClean == excludeClean {
			continue
		}
		tip, ok := merged[wt.branch]
		if !ok {
			continue // not merged into trunk at all
		}
		if mainlineTips[tip] {
			continue // no commits of its own (issue #144, generalized by #382)
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
	return pruned
}

// worktreeHasUncommittedWork reports whether path's tree has anything `git
// status` would show — staged, unstaged, or untracked. It is the ONE check
// in this sweep that reads the live working tree rather than ref state, run
// immediately before the removal it gates: a fresh lane with no commits of
// its own, or a real merge commit having landed, are facts about the
// BRANCH, settled the moment this function is called; "clean" is a fact
// about the WORKTREE that can flip between one sweep and the next, so it is
// never cached or inferred — issue #382, where 15 minutes of uncommitted
// builder work in a worktree the ref checks alone called "merged" was
// destroyed by a sweep that never asked the tree itself.
func worktreeHasUncommittedWork(path string) (bool, error) {
	out, err := git(path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// trunkFirstParentTips is every commit on trunk's OWN mainline — its
// first-parent history — keyed for a tip lookup. A merged branch whose tip
// lands in this set never brought any commit of its own to trunk: either it
// is the literal issue #144 case (a fresh branch created at trunk's current
// tip) or trunk has simply advanced past an older commit the branch never
// moved beyond (issue #382). Either way there is no work here for the sweep
// to have landed, so the branch is skipped exactly as #144 already did —
// just no longer keyed to trunk's CURRENT tip alone. A lane genuinely landed
// by a real merge commit has a tip that is a SECOND parent of one of these
// mainline commits, never a member of the set itself, so this check never
// catches it.
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
// branch, folding in `git worktree prune` so no stale admin record lingers.
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
	_, _ = git(mainRepo, "worktree", "prune")
	_, err := git(mainRepo, "branch", "-D", "--", branch)
	return err
}
