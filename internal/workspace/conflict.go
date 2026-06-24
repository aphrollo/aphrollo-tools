package workspace

import (
	"strings"
	"time"
)

// mergeablePollAttempts and mergeablePollDelay bound the re-poll GitHub forces on
// us: `mergeable` is computed asynchronously, so it reads UNKNOWN for a short
// window right after a push. The read re-polls until it resolves to
// MERGEABLE/CONFLICTING or the bound is hit. They are package vars so tests drive
// the loop without real sleeps. The bound is deliberately small — this runs
// inside the coder's live turn.
var (
	mergeablePollAttempts = 4
	mergeablePollDelay    = 750 * time.Millisecond
)

// isConflicting reports whether GitHub has determined the branch cannot merge
// cleanly: mergeable==CONFLICTING or the finer mergeStateStatus==DIRTY. Both are
// matched case-insensitively. UNKNOWN is NOT a conflict — it is "not computed
// yet" (see mergeUnknown).
func isConflicting(info *PRInfo) bool {
	return strings.EqualFold(info.Mergeable, "CONFLICTING") ||
		strings.EqualFold(info.MergeStateStatus, "DIRTY")
}

// mergeUnknown reports whether GitHub has NOT yet computed mergeability. An empty
// string (an old stub, or a PR opened locally before the field was read) is NOT
// treated as unknown — only the literal UNKNOWN gh returns — so callers that
// never requested the field don't spin the poll.
func mergeUnknown(info *PRInfo) bool {
	return strings.EqualFold(info.Mergeable, "UNKNOWN")
}

// viewPRMergeable reads the branch PR and, while GitHub still reports mergeable
// as UNKNOWN, re-polls a bounded number of times with a small sleep until it
// resolves to MERGEABLE/CONFLICTING. If it stays UNKNOWN within the bound, the
// last (UNKNOWN) PRInfo is returned so the caller reports "unknown — re-run"
// rather than a false all-clear. (nil, nil) when no PR exists for the branch.
func viewPRMergeable(wt, branch string) (*PRInfo, error) {
	var info *PRInfo
	for i := 0; i < mergeablePollAttempts; i++ {
		var err error
		info, err = ghViewPR(wt, branch)
		if err != nil {
			return nil, err
		}
		if info == nil {
			return nil, nil
		}
		if !mergeUnknown(info) {
			return info, nil
		}
		if i < mergeablePollAttempts-1 {
			time.Sleep(mergeablePollDelay)
		}
	}
	return info, nil
}
