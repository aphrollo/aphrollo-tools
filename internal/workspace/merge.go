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
var ghMergePR = func(wt, branch, method string, deleteBranch bool) error {
	args := []string{"pr", "merge", branch, "--" + method}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = wt
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr merge: %v\n%s", err, strings.TrimSpace(string(out)))
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
		fmt.Fprintf(&b, "  deletes the PR branch after merging\n")
	}
	fmt.Fprintf(&b, "  honors GitHub's gates — a non-mergeable or red-CI PR is refused (no force)\n")
	fmt.Fprintf(&b, "\nrun again with --apply to merge (then: aphrollo workspace cleanup %s).\n", m.Target.Branch)
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
	if err := ghMergePR(m.Target.Worktree, m.Target.Branch, m.Method, m.DeleteBranch); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "merged PR #%d (%s): %s\n", pr.Number, m.Method, pr.URL)
	if m.DeleteBranch {
		fmt.Fprintf(stdout, "  deleted branch %s\n", m.Target.Branch)
	}
	fmt.Fprintf(stdout, "  next: aphrollo workspace cleanup %s --apply  (remove the local worktree)\n", m.Target.Branch)
	return nil
}
