package workspace

import (
	"fmt"
	"strconv"
	"strings"
)

// A merge judges one commit and merges that same commit: the PR head GitHub
// reports, resolved once. Everything the gate judges is built from that sha, the
// merge call is bound to it, and a lane whose checked-out HEAD is not that
// commit (or whose worktree holds uncommitted changes) is refused before
// anything is judged, because the lane's local tree is then not what merges
// (#1174: two unpushed commits were judged green while GitHub merged the older
// pushed head). Nothing is pushed on the operator's behalf: that would publish
// work the owner has not seen.

// JudgedHeadError is a refusal that keeps the verdict and the merged tree from
// being two different commits: the lane is not at the PR head, or the head
// moved between judgement and merge. The merge did not happen; the operator
// brings the lane and the PR together and merges again, so the verb exits 2.
type JudgedHeadError struct{ Msg string }

func (e *JudgedHeadError) Error() string { return e.Msg }

// laneSyncPolls is how many polls in a row `merge --wait` lets the lane differ
// from the PR head before it refuses. A push takes a moment to show as the PR's
// head, so one poll or two behind is the push landing; a lane still different
// after that has commits nobody is pushing.
const laneSyncPolls = 3

// laneAtHead is the seam over the check that the lane worktree holds exactly
// the PR head, clean. A package var so merge tests drive the verb's own logic
// without a repository, mirroring the gh seams next to it.
var laneAtHead = laneAtHeadReal

// laneAtHeadReal is laneAtHead's real implementation, named so a test can call
// it regardless of what another test last pointed the var at. prHead is the sha
// GitHub reported and pr the PR's number (to fetch the head when the lane lacks
// it). It returns nil only when the lane's HEAD is prHead and no tracked file
// differs from it; every doubt is a *JudgedHeadError, never a pass.
func laneAtHeadReal(wt, prHead string, pr int) error {
	if !dirExists(wt) {
		return &JudgedHeadError{Msg: fmt.Sprintf("no lane worktree at %s — run the merge from the lane that holds the PR's branch", wt)}
	}
	laneHead, err := laneHeadSHA(wt)
	if err != nil {
		return &JudgedHeadError{Msg: fmt.Sprintf("lane %s is not readable (%v) — run the merge from the lane that holds the PR's branch", wt, err)}
	}
	if laneHead == prHead {
		out, err := wtGit(wt, "status", "--porcelain", "--untracked-files=no") // stderr-ok: a failed status is reported as the refusal below
		if err != nil {
			return &JudgedHeadError{Msg: fmt.Sprintf("could not read the lane's uncommitted changes (%v), so it is not known to be the PR head %s", err, short(prHead))}
		}
		if strings.TrimSpace(string(out)) != "" {
			return &JudgedHeadError{Msg: fmt.Sprintf("lane has uncommitted changes on %s, the PR head — commit and push them (aphrollo workspace commit, then push) or discard them, and merge again", short(prHead))}
		}
		return nil
	}
	if !wtGitOK(wt, "cat-file", "-e", prHead+"^{commit}") {
		// The lane never fetched the PR's head (someone else pushed it, or it
		// is a different branch than the lane's own): ask GitHub for it.
		_, _ = wtNetwork(wt, "fetch", "--quiet", "origin", "refs/pull/"+strconv.Itoa(pr)+"/head")
		if !wtGitOK(wt, "cat-file", "-e", prHead+"^{commit}") {
			return &JudgedHeadError{Msg: fmt.Sprintf("lane HEAD %s is not the PR head %s, and the lane does not hold that commit — fetch it (git fetch origin) and merge again", short(laneHead), short(prHead))}
		}
	}
	out, err := wtGit(wt, "rev-list", "--left-right", "--count", laneHead+"..."+prHead) // stderr-ok: a failed count is reported as the refusal below
	if err != nil {
		return &JudgedHeadError{Msg: fmt.Sprintf("lane HEAD %s is not the PR head %s, and the commits between them could not be counted (%v)", short(laneHead), short(prHead), err)}
	}
	var ahead, behind int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &ahead, &behind); err != nil {
		return &JudgedHeadError{Msg: fmt.Sprintf("lane HEAD %s is not the PR head %s, and the commits between them could not be counted (%q)", short(laneHead), short(prHead), strings.TrimSpace(string(out)))}
	}
	return &JudgedHeadError{Msg: laneDriftLine(laneHead, prHead, ahead, behind)}
}

// laneDriftLine is the one line saying how the lane differs from the PR head,
// and what to do about it.
func laneDriftLine(laneHead, prHead string, ahead, behind int) string {
	head := fmt.Sprintf("lane HEAD %s is not the PR head %s", short(laneHead), short(prHead))
	switch {
	case ahead > 0 && behind > 0:
		return fmt.Sprintf("%s (%d local %s not pushed, the PR has %d %s the lane lacks) — rebase onto the PR head, push (aphrollo workspace push) and merge again",
			head, ahead, commitNoun(ahead), behind, commitNoun(behind))
	case ahead > 0:
		return fmt.Sprintf("%s (%d local %s not pushed) — push (aphrollo workspace push) and merge again", head, ahead, commitNoun(ahead))
	case behind > 0:
		return fmt.Sprintf("%s (the PR has %d %s the lane lacks) — pull them (git pull --ff-only) and merge again", head, behind, commitNoun(behind))
	}
	return head + " — merge again"
}

func commitNoun(n int) string {
	if n == 1 {
		return "commit"
	}
	return "commits"
}

// headMoved reads a merge refusal as GitHub saying the PR head is no longer the
// commit the merge was bound to, and returns the one-line refusal; nil when it
// is any other failure.
func headMoved(out []byte, sha string) error {
	if !strings.Contains(strings.ToLower(string(out)), "head branch was modified") {
		return nil
	}
	return &JudgedHeadError{Msg: fmt.Sprintf("PR head moved after it was judged (merge bound to %s) — merge again to judge the new head", short(sha))}
}
