package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// The merge verb's half of the undercover walls. GitHub's default squash
// message lists every commit author as a co-author, which is how a tool
// identity once reached main through a clean-looking PR. In a repo that sets
// `undercover = true` the merge therefore refuses a PR carrying a tell in any
// commit's author, committer or Co-authored-by trailer, and passes the
// squash (or merge) body explicitly, so GitHub never generates one.

// ghPRText is the seam over the PR's title and body as they landed.
var ghPRText = func(wt, branch string) (title, body string, err error) {
	out, err := ghCombinedOutput(wt, "pr", "view", "--json", "title,body", "--", branch)
	if err != nil {
		return "", "", fmt.Errorf("gh pr view %s: %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	var pr struct{ Title, Body string }
	if err := json.Unmarshal(out, &pr); err != nil {
		return "", "", fmt.Errorf("parsing gh pr view: %w", err)
	}
	return pr.Title, pr.Body, nil
}

// ghMergePRBody is ghMergePR with an explicit commit body.
var ghMergePRBody = func(wt, branch, method, body string) error {
	args := []string{"pr", "merge", "--" + method, "--body", body, "--", branch}
	out, err := ghCombinedOutput(wt, args...)
	if err != nil {
		return fmt.Errorf("gh pr merge: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// undercoverMerge judges the PR before it merges. useBody reports that the
// merge must pass body explicitly: the repo set `undercover = true` and the
// method writes a commit body (a rebase writes none). A repo that never
// asked merges exactly as before.
func undercoverMerge(t *Target, method string) (body string, useBody bool, err error) {
	tells, on := undercover.Load(t.Worktree)
	if !on {
		return "", false, nil
	}
	if err := undercoverPRCommits(t, tells); err != nil {
		return "", false, err
	}
	if method == "rebase" {
		return "", false, nil
	}
	title, raw, err := ghPRText(t.Worktree, t.Branch)
	if err != nil {
		return "", false, err
	}
	kept, _ := undercover.StripFooter(raw, tells)
	if h, hit := tells.Text(kept); hit {
		return "", false, errors.New(undercover.TextRefusal("PR body", h))
	}
	if strings.TrimSpace(kept) == "" {
		kept = title
	}
	base := "origin/" + resolveDefaultBranch(t.Worktree)
	return withClosingTrailers(kept, commitMessagesSince(t.Worktree, base, "HEAD")), true, nil
}

// missingCloses is every issue a lane commit closes that body does not, in
// first-seen order.
func missingCloses(body string, commits []string) []string {
	have := map[string]bool{}
	for _, ref := range tdd.ClosingRefs(body) {
		have[ref] = true
	}
	var missing []string
	for _, ref := range tdd.ClosingRefs(commits...) {
		if !have[ref] {
			missing = append(missing, ref)
		}
	}
	return missing
}

// withClosingTrailers appends a `Closes <ref>` trailer for every issue a lane
// commit closes and body does not. A squash merge writes only this body to
// main, and GitHub closes an issue only from a keyword in it, so a trailer
// left behind in a lane commit would leave its issue open (#952).
func withClosingTrailers(body string, commits []string) string {
	missing := missingCloses(body, commits)
	if len(missing) == 0 {
		return body
	}
	trailers := make([]string, len(missing))
	for i, ref := range missing {
		trailers[i] = "Closes " + ref
	}
	return strings.TrimRight(body, "\n") + "\n\n" + strings.Join(trailers, "\n")
}

// undercoverPRCommits refuses when a commit the PR brings — everything on the
// branch that origin's default branch does not hold — carries a tell.
func undercoverPRCommits(t *Target, tells undercover.List) error {
	base := "origin/" + resolveDefaultBranch(t.Worktree)
	sha, h, hit, err := tells.RangeTell("git", t.Worktree, nil, base+"..HEAD")
	if err != nil {
		return fmt.Errorf("listing the PR's commits (%s..HEAD): %w", base, err)
	}
	if !hit {
		return nil
	}
	return fmt.Errorf("undercover: commit %.12s's %s %q carries %q, and a squash merge would copy it into main; "+
		"set your own identity (git config user.name/user.email), rewrite the branch "+
		"(git rebase --exec 'git commit --amend --no-edit --reset-author' %s) and push it again",
		sha, h.Field, h.Value, h.Tell, base)
}
