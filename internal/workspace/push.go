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

	// prInfo/prInfoErr and ci/ciErr cache the mergeable-poll and CI-check reads
	// Apply performs to render its own (often-suppressed) receipt lines. Submit
	// reuses them instead of re-issuing the same bounded mergeable poll and the
	// same `gh pr checks` call a second time — see Submit.Apply.
	prInfo    *PRInfo
	prInfoErr error
	ci        CIStatus
	ciErr     error
}

// PushPlan resolves the push without executing it: whether an upstream exists and
// how many commits are pending. It refuses a detached HEAD — there is no branch
// to push.
func PushPlan(t *Target, forceWithLease bool) (*Push, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before pushing", t.Worktree)
	}
	p := &Push{Target: t, ForceWithLease: forceWithLease}
	p.hasUpstream, p.ahead = resolveAhead(t.Worktree, t.Branch)
	return p, nil
}

// resolveAhead reports whether branch has a configured upstream and how many
// commits it is ahead of it (or of origin/branch, for a branch already on
// origin with no upstream configured locally). Shared by PushPlan — which
// resolves it for the dry-run preview — and Push.Apply, which recomputes it
// fresh immediately before pushing rather than trusting that Plan-time
// snapshot: ship builds the push stage's plan before its commit stage creates
// the commit being shipped, so the snapshot predates it and would print a
// stale count (#162).
func resolveAhead(wt, branch string) (hasUpstream bool, ahead string) {
	if upstreamRef(wt) != "" {
		return true, aheadCount(wt, "@{u}")
	}
	if remoteBranchExists(wt, branch) {
		return false, aheadCount(wt, "origin/"+branch)
	}
	return false, ""
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
	fmt.Fprintf(&b, "\nrun again without --dry to push.\n")
	return b.String()
}

// pushArgs builds the `git push` argv. --force-with-lease must come before the
// "--" end-of-options terminator: after "--" git treats every token as a
// refspec, so a trailing flag would be read as the literal refspec
// "--force-with-lease" and fail with "src refspec ... does not match any". The
// "--" still guards the branch name from being parsed as a flag (defense in
// depth behind Slugify's leading-dash rejection).
func pushArgs(branch string, forceWithLease bool) []string {
	args := []string{"push", "-u"}
	if forceWithLease {
		args = append(args, "--force-with-lease")
	}
	return append(args, "origin", "--", branch)
}

// Apply pushes HEAD to origin (setting upstream) and reports the stateful
// receipt: the pushed line, the PR line (if one already exists), and the CI
// state, so a coder needs no follow-up git/gh call to confirm what landed. Push
// never OPENS a PR — submit is the sole opener — so CI fires exactly once, at
// the handoff, instead of once on a draft's `opened` event and again on
// `ready_for_review`.
func (p *Push) Apply(stdout, stderr io.Writer) error {
	wt, branch := p.Target.Worktree, p.Target.Branch
	// Refresh immediately before pushing — see resolveAhead's doc comment for
	// why the Plan-time snapshot can be stale by the time Apply runs.
	p.hasUpstream, p.ahead = resolveAhead(wt, branch)
	// git's own progress goes to stderr, keeping stdout the parseable receipt.
	if err := gitNetworkStream(wt, stderr, stderr, pushArgs(branch, p.ForceWithLease)...); err != nil {
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

	// Reuse an existing OPEN PR's state in the receipt; open nothing when none
	// exists yet — that is submit's job.
	info, err := reuseOpenPR(wt, branch)
	if err != nil {
		return err
	}
	if info == nil {
		fmt.Fprintf(stdout, "no PR yet for %s — run: aphrollo workspace submit\n", branch)
	} else {
		fmt.Fprintf(stdout, "pr #%d %s [reused] %s\n", info.Number, prStateWord(info), info.URL)
		reportPRState(stdout, info)
	}

	// Surface merge conflicts on every push — push always runs, so a coder who only
	// pushes still sees them. Non-fatal: push's job is to publish. Re-poll past
	// GitHub's async UNKNOWN window so a fresh push isn't a false all-clear.
	// Cached on p so Submit.Apply can reuse this exact read instead of re-polling.
	mi, miErr := viewPRMergeable(wt, branch)
	p.prInfo, p.prInfoErr = mi, miErr
	if miErr == nil && mi != nil {
		switch {
		case isConflicting(mi):
			fmt.Fprintf(stdout, "CONFLICT: branch has merge conflicts — rebase onto %s and resolve before submit\n", resolveDefaultBranch(wt))
		case mergeUnknown(mi):
			fmt.Fprintf(stdout, "mergeable: unknown — re-run to recheck\n")
		}
	}

	// Read CI so the receipt carries it without a follow-up call. Cached on p
	// for the same reason as prInfo above.
	ci, ciErr := ghCIStatus(wt, branch)
	p.ci, p.ciErr = ci, ciErr
	if ciErr == nil {
		fmt.Fprintf(stdout, "ci %s\n", ci.State)
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
