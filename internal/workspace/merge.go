package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// mergeMethods are gh's merge strategies. The repo squash-merges (PR subjects
// end in "(#N)"), so squash is the default.
var mergeMethods = map[string]bool{"squash": true, "merge": true, "rebase": true}

// ghMergePR is the seam over `gh pr merge`, a package var so tests drive merge's
// logic without gh or the network. The real implementation merges in the
// worktree (gh resolves the repo from its origin).
//
// It deliberately does NOT pass `--delete-branch`: gh's branch deletion checks
// out the default branch first (you can't delete the branch you're on), and
// inside a worktree that switch fails — `fatal: 'main' is already used by
// worktree …` — because the main clone holds main. The remote PR merge succeeds
// but the verb returns non-zero on gh's local checkout. Branch deletion is split
// out to ghDeleteRemoteBranch, which never checks anything out.
var ghMergePR = func(wt, branch, method string) error {
	// Flags first, then "--" so the branch is always a positional and never
	// parsed as an option (defense in depth behind Slugify).
	args := []string{"pr", "merge", "--" + method, "--", branch}
	cmd := exec.Command("gh", args...)
	cmd.Dir = wt
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr merge: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ghDeleteRemoteBranch deletes the PR's head branch on the remote with
// `git push origin --delete`, a ref update that touches no working tree — so it
// is safe from inside a worktree, unlike gh's `--delete-branch` (see ghMergePR).
// The local branch and worktree are left to `cleanup`. A branch GitHub already
// reaped (repos with auto-delete-on-merge) is treated as success.
var ghDeleteRemoteBranch = func(wt, branch string) error {
	cmd := exec.Command("git", "-C", wt, "push", "origin", "--delete", "--", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "does not exist") {
			return nil // already gone — nothing to delete
		}
		return fmt.Errorf("git push origin --delete %s: %v\n%s", branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Merge is a resolved merge of the worktree branch's PR. It honors GitHub's own
// gates: gh refuses a PR that is not mergeable or whose required checks are
// red, so this never force-merges (no --admin). Merge does NOT touch the local
// worktree — run `cleanup` for that, after (or instead of) merging.
type Merge struct {
	Target       *Target
	Method       string // squash | merge | rebase
	DeleteBranch bool   // delete the PR branch after merging
}

// MergePlan validates the merge without touching gh; the PR is resolved at Apply
// time (like PRPlan), so the dry-run stays offline and tests stay hermetic.
func MergePlan(t *Target, method string, deleteBranch bool) (*Merge, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out the PR branch before merging", t.Worktree)
	}
	if !mergeMethods[method] {
		return nil, fmt.Errorf("merge method not allowed: %s (want squash|merge|rebase)", method)
	}
	return &Merge{Target: t, Method: method, DeleteBranch: deleteBranch}, nil
}

// Render previews the merge.
func (m *Merge) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace merge: PR for %s  (%s)\n", m.Target.Branch, m.Method)
	if apply {
		return b.String()
	}
	if m.DeleteBranch {
		fmt.Fprintf(&b, "  deletes the PR's remote branch after merging (local worktree left for cleanup)\n")
	}
	fmt.Fprintf(&b, "  honors GitHub's gates — a non-mergeable or red-CI PR is refused (no force)\n")
	fmt.Fprintf(&b, "\nrun again without --dry to merge (then: aphrollo workspace cleanup %s).\n", m.Target.Branch)
	return b.String()
}

// Apply resolves the branch's open PR and merges it, printing the outcome. A
// branch with no PR points at `pr`; the merge itself is gh's call.
func (m *Merge) Apply(stdout, stderr io.Writer) error {
	pr, err := ghViewPR(m.Target.Worktree, m.Target.Branch)
	if err != nil {
		return err
	}
	if pr == nil {
		return fmt.Errorf("no open PR for %s — run: aphrollo workspace pr", m.Target.Branch)
	}
	if err := ghMergePR(m.Target.Worktree, m.Target.Branch, m.Method); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "merged PR #%d (%s): %s\n", pr.Number, m.Method, pr.URL)
	if m.DeleteBranch {
		if err := ghDeleteRemoteBranch(m.Target.Worktree, m.Target.Branch); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "  deleted remote branch %s (local worktree left for cleanup)\n", m.Target.Branch)
	}
	fmt.Fprintf(stdout, "  next: aphrollo workspace cleanup %s --apply  (remove the local worktree)\n", m.Target.Branch)
	return nil
}
