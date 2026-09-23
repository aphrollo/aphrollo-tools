package tdd

import (
	"strings"
)

// laneChangedPaths lists the paths the lane has changed against its base
// branch, repo-root-relative. It is the merge gate's own scope: what this
// branch will carry into main — which can never include trunk's OWN commits.
//
// The base comes from laneBaseSHA, the resolution that weighs every trunk
// candidate rather than taking `origin/main` on sight. Against that
// publication point, a repo whose local main is ahead of its last push reads
// MAIN'S own unpushed commits as the lane's work: the format check charged a
// lane for trunk's files and the baseline guard read rows trunk itself wrote
// as this lane raising a ceiling.
func laneChangedPaths(repoRoot string) []string {
	merge := laneBaseSHA(repoRoot)
	if merge == "" {
		return nil
	}
	out, err := git(repoRoot, "diff", "--name-only", "-M", "--diff-filter="+stagedDiffFilter, merge, "HEAD")
	if err != nil {
		return nil
	}
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}
