package tdd

import (
	"path/filepath"
	"sort"
	"strings"
)

// baselineCompareRef names the content a baseline is judged against: the point
// the lane branched from its base, not HEAD.
//
// Comparing against HEAD asked "did THIS COMMIT raise the ceiling", and a raise
// landed by an earlier commit in the same lane is already in HEAD — so every
// later commit compared it against itself and found nothing, while the merge
// gate compares the lane against main and rejects it (escape #206). A baseline
// only ever goes down relative to what the lane will merge INTO.
//
// Where no base resolves — a repo with a single commit, a detached checkout
// with no branch to compare to — it falls back to HEAD, which is the old
// behaviour and still catches a raise staged in the commit at hand.
func baselineCompareRef(repoRoot string) string {
	base := laneBaseRef(repoRoot)
	merge, err := git(repoRoot, "merge-base", base, "HEAD")
	if err != nil {
		return "HEAD"
	}
	if merge = strings.TrimSpace(merge); merge == "" {
		return "HEAD"
	}
	return merge
}

// baselineFilesInLane is the set the guard judges: the baselines this commit
// stages, plus the ones an earlier commit in the lane already changed. Both
// halves are needed — the staged half catches the raise as it is made, and the
// lane half keeps catching it on every commit after, which is where the merge
// gate's verdict actually comes from.
func baselineFilesInLane(repoRoot string) []string {
	seen := map[string]bool{}
	var files []string
	for _, f := range stagedFiles(repoRoot) {
		rel := filepath.ToSlash(f)
		if !seen[rel] {
			seen[rel] = true
			files = append(files, rel)
		}
	}
	var extra []string
	for _, f := range laneChangedPaths(repoRoot) {
		rel := filepath.ToSlash(f)
		if !seen[rel] {
			seen[rel] = true
			extra = append(extra, rel)
		}
	}
	sort.Strings(extra)
	return append(files, extra...)
}
