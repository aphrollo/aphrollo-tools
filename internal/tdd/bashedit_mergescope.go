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

// A `git commit` (or `git merge --continue`) that concludes a merge moves
// every path the merge staged out of the dirty set, and so does a `git merge
// --abort` that puts them back to HEAD. None of them was edited by that
// command: a conclusion had git run the pre-merge routine over them as it
// committed, and an abort restores what HEAD already held. The harvest read
// "left the dirty set" as an edit and ran their suites again, on the primary
// and in lanes alike.

// mergeHeadCommit is the commit MERGE_HEAD names, "" when no merge is in
// progress.
func mergeHeadCommit(root string) string {
	out, err := gitRead(root, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// headCommit is the commit HEAD names, "" before the first commit.
func headCommit(root string) string {
	out, err := gitRead(root, "rev-parse", "-q", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// withoutEndedMergePaths drops, from changed, the paths a command that ended
// a merge moved out of the dirty set: dirty before, clean now, when a merge
// was in progress before and is not now, and HEAD either became a merge of
// the commit MERGE_HEAD named (concluded) or did not move (aborted). A path
// the command also edited stays dirty with a new stamp and is kept; so is one
// that entered the dirty set. how names which ending it was; dropped counts
// what was removed.
func withoutEndedMergePaths(before, now *bashSnapshot, changed []string) (kept []string, dropped int, how string) {
	if now == nil || before.MergeHead == "" || now.MergeHead != "" {
		return changed, 0, ""
	}
	switch {
	case headMerged(before.Root, before.MergeHead):
		how = "concluded"
	case before.Head != "" && now.Head == before.Head:
		how = "aborted"
	default:
		return changed, 0, ""
	}
	for _, rel := range changed {
		_, wasDirty := before.Dirty[rel]
		_, isDirty := now.Dirty[rel]
		if wasDirty && !isDirty {
			dropped++
			continue
		}
		kept = append(kept, rel)
	}
	return kept, dropped, how
}

// headMerged reports whether HEAD is a merge commit with incoming among its
// parents.
func headMerged(root, incoming string) bool {
	out, err := gitRead(root, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		return false
	}
	parents := strings.Fields(out)
	if len(parents) < 3 {
		return false
	}
	for _, p := range parents[1:] {
		if p == incoming {
			return true
		}
	}
	return false
}

// mergeEndedLine is what a harvest says when every changed path was moved by
// ending a merge rather than by an edit.
func mergeEndedLine(root, how string, dropped int) string {
	return fmt.Sprintf("gate: merge %s (%d merged path(s) left the dirty set in %s) — nothing edited, no suite run", how, dropped, root)
}
