package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Push is a resolved push of a worktree's branch to origin. It sets the upstream
// on a first push and reports the ahead-count and the branch's web URL, so a
// follow-up `pr` (or a human) has the link without another round trip.
type Push struct {
	Target         *Target
	ForceWithLease bool
	hasUpstream    bool
	ahead          string // commits the local branch is ahead ("" => unknown/new)
}

// PushPlan resolves the push without executing it: whether an upstream exists and
// how many commits are pending. It refuses a detached HEAD — there is no branch
// to push.
func PushPlan(t *Target, forceWithLease bool) (*Push, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before pushing", t.Worktree)
	}
	p := &Push{Target: t, ForceWithLease: forceWithLease}
	if upstreamRef(t.Worktree) != "" {
		p.hasUpstream = true
		p.ahead = aheadCount(t.Worktree, "@{u}")
	} else if remoteBranchExists(t.Worktree, t.Branch) {
		p.ahead = aheadCount(t.Worktree, "origin/"+t.Branch)
	}
	return p, nil
}

// Render previews the push. apply=false is the dry-run; apply=true the header.
func (p *Push) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace push: %s -> origin  (cwd %s)\n", p.Target.Branch, p.Target.Worktree)
	fmt.Fprintf(&b, "  %s\n", p.state())
	if apply {
		return b.String()
	}
	if p.ForceWithLease {
		fmt.Fprintf(&b, "  --force-with-lease\n")
	}
	fmt.Fprintf(&b, "\nrun again with --apply to push.\n")
	return b.String()
}

// Apply pushes HEAD to origin (setting upstream), then prints the outcome plus
// the branch URL.
func (p *Push) Apply(stdout, stderr io.Writer) error {
	wt, branch := p.Target.Worktree, p.Target.Branch
	// "--" terminates options so a branch name can never be parsed as a git
	// flag (defense in depth behind Slugify's leading-dash rejection).
	args := []string{"-C", wt, "push", "-u", "origin", "--", branch}
	if p.ForceWithLease {
		args = append(args, "--force-with-lease")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	suffix := ""
	if p.ahead != "" && p.ahead != "0" {
		suffix = fmt.Sprintf(" (%s commit(s))", p.ahead)
	}
	fmt.Fprintf(stdout, "pushed %s -> origin%s\n", branch, suffix)
	if url := branchURL(wt, branch); url != "" {
		fmt.Fprintf(stdout, "  %s\n", url)
	}
	return nil
}

func (p *Push) state() string {
	switch {
	case !p.hasUpstream && p.ahead == "":
		return "new branch — will set upstream origin/" + p.Target.Branch
	case p.ahead == "" || p.ahead == "0":
		return "up to date with origin/" + p.Target.Branch
	default:
		return p.ahead + " commit(s) ahead of origin/" + p.Target.Branch
	}
}

// --- git helpers ------------------------------------------------------------

// upstreamRef returns the configured upstream (e.g. "origin/feat") or "" if the
// branch has none.
func upstreamRef(wt string) string {
	out, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func remoteBranchExists(wt, branch string) bool {
	return exec.Command("git", "-C", wt, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch).Run() == nil
}

// aheadCount returns how many commits HEAD is ahead of base, as a string.
func aheadCount(wt, base string) string {
	out, err := exec.Command("git", "-C", wt, "rev-list", "--count", base+"..HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// branchURL turns origin's remote URL into a github tree URL for the branch,
// normalizing both ssh and https forms. Returns "" for a non-github remote.
func branchURL(wt, branch string) string {
	out, err := exec.Command("git", "-C", wt, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	repo := normalizeGitHubURL(strings.TrimSpace(string(out)))
	if repo == "" {
		return ""
	}
	return repo + "/tree/" + branch
}

// normalizeGitHubURL reduces a git remote URL to its https web base
// (https://github.com/owner/repo), stripping the .git suffix and the
// git@/ssh:// forms. Returns "" when the remote isn't a github URL.
func normalizeGitHubURL(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		return "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		return "https://github.com/" + strings.TrimPrefix(remote, "ssh://git@github.com/")
	case strings.HasPrefix(remote, "https://github.com/"):
		return remote
	default:
		return ""
	}
}
