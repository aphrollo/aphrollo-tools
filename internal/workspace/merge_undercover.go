package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// undercoverMerge judges the PR before it merges. on is false when the repo
// never asked, and the merge then runs exactly as before. body is the
// explicit commit body, "" for a rebase, which writes none.
func undercoverMerge(t *Target, method string) (body string, on bool, err error) {
	tells, on := undercover.Load(t.Worktree)
	if !on {
		return "", false, nil
	}
	if err := undercoverPRCommits(t, tells); err != nil {
		return "", true, err
	}
	if method == "rebase" {
		return "", true, nil
	}
	title, raw, err := ghPRText(t.Worktree, t.Branch)
	if err != nil {
		return "", true, err
	}
	kept, _ := undercover.StripFooter(raw, tells)
	if h, hit := tells.Text(kept); hit {
		return "", true, errors.New(undercover.TextRefusal("PR body", h))
	}
	if strings.TrimSpace(kept) == "" {
		kept = title
	}
	return kept, true, nil
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
