package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// Why `--merged` listed no lane after a squash merge. A lane the merge queue
// landed as a squash commit has a tip that never becomes an ancestor of trunk:
// trunk holds one new commit with the lane's changes in it, and the lane's own
// commits stay off to the side. `for-each-ref --merged` reads ancestry only, so
// the sweep examined 87 lanes after such merges and pruned none of them, and
// every lane kept its worktree, its node_modules and its venv.
//
// A lane that is not an ancestor counts as landed on either of two facts, each
// checked for the lane's own tip:
//
//  1. Trunk already holds what the lane brings. A merge of the lane into trunk,
//     made in memory by `git merge-tree`, is clean and leaves trunk's own tree
//     unchanged: every change the lane made is in trunk.
//  2. The merge verb recorded the lane as merged (a merge event with its lane
//     and verdict ok, written by `workspace merge` and by the queue's landing),
//     and the lane's newest commit is not newer than that record. Trunk may
//     have moved on and rewritten the lane's files since, which the first fact
//     cannot judge; a commit made after the record is work trunk never saw.
//
// A branch that is not an ancestor always has a commit trunk lacks, so neither
// fact can read a lane that never carried work (issues #144, #382, #644) as
// landed.

// laneQuietFor is how long a session must have left a lane alone before the
// sweep may remove it. The kernel's own settle time for a merged lane (§3).
const laneQuietFor = 30 * time.Minute

// recordedLaneMerges is, per lane branch, the time of each merge the event log
// records for it.
func recordedLaneMerges(mainRepo string) map[string][]time.Time {
	out := map[string][]time.Time{}
	for _, e := range core.ReadEvents(mainRepo) {
		if e.Kind != "merge" || e.Verdict != "ok" || e.Lane == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339, e.At); err == nil {
			out[e.Lane] = append(out[e.Lane], at)
		}
	}
	return out
}

// squashLandedHow names the evidence that branch landed on trunk although its
// tip is not an ancestor of it, or "" when there is none.
func squashLandedHow(mainRepo, trunk, branch string, recorded map[string][]time.Time) string {
	ref := "refs/heads/" + branch
	if times := recorded[branch]; len(times) > 0 {
		if tip, ok := tipCommitTime(mainRepo, ref); ok {
			for _, at := range times {
				if !tip.After(at) {
					return "its merge is recorded"
				}
			}
		}
	}
	if laneTreeInTrunk(mainRepo, trunk, ref) {
		return "trunk holds its changes"
	}
	return ""
}

func tipCommitTime(mainRepo, ref string) (time.Time, bool) {
	sec, err := strconv.ParseInt(gitOut(mainRepo, "log", "-1", "--format=%ct", ref), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}

// laneTreeInTrunk reports whether merging ref into trunk changes nothing: the
// merge is clean and its tree is trunk's. git writes the merged tree's objects
// into the object store, unreferenced, for its own gc to take. A git without
// merge-tree --write-tree, or a conflict, answers no.
func laneTreeInTrunk(mainRepo, trunk, ref string) bool {
	merged, _, _ := strings.Cut(gitOut(mainRepo, "merge-tree", "--write-tree", "--no-messages", trunk, ref), "\n")
	merged = strings.TrimSpace(merged)
	return merged != "" && merged == gitOut(mainRepo, "rev-parse", trunk+"^{tree}")
}

// laneHolds is what keeps a landed lane in place: a worktree git has locked
// (the agent harness locks the ones it hands a session), a lane the dev tier
// serves, and a lane a session stamped a result in lately. A removal under any
// of them takes the ground away from a builder.
type laneHolds struct {
	locked  map[string]string // cleaned worktree path to git's lock reason
	claimed map[string]bool   // cleaned worktree path a .devclaim link points at
	now     time.Time
}

func newLaneHolds(mainRepo string, now time.Time) laneHolds {
	return laneHolds{locked: lockedWorktrees(mainRepo), claimed: devClaimedWorktrees(mainRepo), now: now}
}

// reason is why wt must stay, or "".
func (h laneHolds) reason(wt string) string {
	key := cleanWorktreePath(wt)
	if why, ok := h.locked[key]; ok {
		if why != "" {
			return "worktree is locked: " + why
		}
		return "worktree is locked"
	}
	if h.claimed[key] {
		return "claimed on the dev tier (.devclaim)"
	}
	if at, ok := core.LastSessionActivityIn(wt); ok && h.now.Sub(at) < laneQuietFor {
		return fmt.Sprintf("a session worked in it %d min ago", int(h.now.Sub(at).Minutes()))
	}
	return ""
}

// lockedWorktrees reads the `locked` lines of `git worktree list --porcelain`.
func lockedWorktrees(mainRepo string) map[string]string {
	out := map[string]string{}
	path := ""
	for line := range strings.Lines(gitOut(mainRepo, "worktree", "list", "--porcelain")) {
		line = strings.TrimRight(line, "\r\n")
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if rest, ok := strings.CutPrefix(line, "locked"); ok && path != "" {
			out[cleanWorktreePath(path)] = strings.TrimSpace(rest)
		}
	}
	return out
}

// devClaimedWorktrees is every worktree a link in the repo's .devclaim
// directory points at. The directory is where workspace's claim keeps it: the
// APHROLLO_DEVCLAIM_DIR override, else beside the repo.
func devClaimedWorktrees(mainRepo string) map[string]bool {
	dir := os.Getenv("APHROLLO_DEVCLAIM_DIR")
	if dir == "" {
		dir = filepath.Join(filepath.Dir(mainRepo), ".devclaim")
	}
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if target, err := os.Readlink(filepath.Join(dir, e.Name())); err == nil {
			out[cleanWorktreePath(target)] = true
		}
	}
	return out
}
