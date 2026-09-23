package tdd

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A trunk sync into a lane (mergescope.go's trunkSyncTip) is judged on the
// lane's own contribution at the merge gate. The Bash harvest has to scope
// the same way: a conflicted `git merge main` leaves everything trunk gained
// since the fork staged in the lane, and every one of those paths enters the
// dirty set, so the harvest read trunk's already-judged work as this
// command's edits and ran suites over it (issue #726).

// trunkSyncOwnPaths drops, from changed, every path the trunk sync in
// progress brought in unchanged: tracked at the incoming trunk tip and
// identical to it in the working tree. What is left is the lane's own
// contribution (its changes, a conflicted file, a resolution being written)
// and any path trunk does not have. dropped counts what was removed. Outside
// a trunk sync, or when git cannot answer, changed comes back whole.
func trunkSyncOwnPaths(root string, changed []string) (own []string, dropped int) {
	tip, ok := trunkSyncTip(root)
	if !ok || len(changed) == 0 {
		return changed, 0
	}
	atTip, err := gitPathSet(root, append([]string{"ls-tree", "-r", "--name-only", tip, "--"}, changed...))
	if err != nil {
		return changed, 0
	}
	differ, err := gitPathSet(root, append([]string{"diff", "--name-only", "--no-renames", tip, "--"}, changed...))
	if err != nil {
		return changed, 0
	}
	for _, rel := range changed {
		key := filepath.ToSlash(rel)
		if atTip[key] && !differ[key] {
			dropped++
			continue
		}
		own = append(own, rel)
	}
	return own, dropped
}

// gitPathSet runs a git command that prints one repo-relative path per line
// and returns them as a set.
func gitPathSet(root string, args []string) (map[string]bool, error) {
	out, err := gitRead(root, args...)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set, nil
}

// trunkSyncStandDownLine is what a harvest says when every changed path was
// trunk's: nothing of this lane's moved, and the merge gate judges the
// lane's contribution when the sync is committed.
func trunkSyncStandDownLine(root string, dropped int) string {
	return fmt.Sprintf("gate: trunk sync in progress (%d path(s) brought in from trunk in %s, already judged there) — premerge runs at commit", dropped, root)
}
