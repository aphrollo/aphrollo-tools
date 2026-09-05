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
// law in this package uses) — `git branch --merged <trunk>` — with one
// narrowing: a branch whose tip IS trunk's own tip never counts as merged,
// even though `--merged` alone would say so. A branch created moments
// earlier, with no commits of its own yet, trivially satisfies "merged" that
// way — its tip already equals trunk's — without having landed any work at
// all. That gap is issue #144: a fresh lane, still clean because a builder
// had not yet made an edit in it, was pruned out from under them by a sweep
// that read "clean" as "merged".
//
// exclude is the worktree running THIS merge (or "" to exclude none) — the
// ground under the process calling this, which must never be swept
// regardless of its own branch's merge state.
//
// A merged branch is, by definition, work already landed on trunk, so there
// is nothing an --apply opt-in would protect that `--merged` does not
// already guarantee; every prune is announced on stdout as it happens.
// Removal errors are reported on stderr and do not stop the sweep — one
// worktree a lock or a permission refuses is not a reason to leave every
// other landed lane behind too.
func PruneMergedLanesAfterMerge(mainRepo, exclude string, stdout, stderr io.Writer) []PrunedLane {
	if mainRepo == "" {
		return nil
	}
	trunk := trunkBranch(mainRepo)
	if trunk == "" {
		return nil
	}
	trunkTip := gitOut(mainRepo, "rev-parse", trunk)
	if trunkTip == "" {
		return nil
	}
	merged := mergedBranchTips(mainRepo, trunk)
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
		if !ok || tip == trunkTip {
			continue // not merged, or a fresh branch sitting at trunk's own tip
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
func removeMergedLaneWorktree(mainRepo, path, branch string) error {
	if _, err := git(mainRepo, "worktree", "remove", path); err != nil {
		return err
	}
	_, _ = git(mainRepo, "worktree", "prune")
	_, err := git(mainRepo, "branch", "-D", "--", branch)
	return err
}
