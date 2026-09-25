package workspace

import (
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
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
	out, err := ghCombinedOutput(wt, args...)
	if err != nil {
		return fmt.Errorf("gh pr merge: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// remoteBranchAlreadyGone reports whether a failed `git push origin --delete`
// means the branch was already absent on the remote rather than that the
// delete itself failed. Two message shapes are known: git's own "does not
// exist", and the one a repo with auto-delete-head-branch produces after a
// squash merge already reaped the branch by the time this call lands —
// "! [remote rejected] lane/x (cannot lock ref 'refs/heads/lane/x': unable to
// resolve reference 'refs/heads/lane/x')" (issue #410, PRs #402 and #408).
func remoteBranchAlreadyGone(output string) bool {
	return strings.Contains(output, "does not exist") ||
		strings.Contains(output, "unable to resolve reference")
}

// ghDeleteRemoteBranch deletes the PR's head branch on the remote with
// `git push origin --delete`, a ref update that touches no working tree — so it
// is safe from inside a worktree, unlike gh's `--delete-branch` (see ghMergePR).
// The local branch and worktree are left to `prune`. A branch GitHub already
// reaped (repos with auto-delete-on-merge) is treated as success, and the
// bool return tells the caller so it reports "[skip]" rather than claiming a
// deletion that never happened.
var ghDeleteRemoteBranch = func(wt, branch string) (bool, error) {
	out, err := gitNetworkOutput(wt, "push", "origin", "--delete", "--", branch)
	if err != nil {
		if remoteBranchAlreadyGone(string(out)) {
			return true, nil // already gone — nothing to delete
		}
		return false, fmt.Errorf("git push origin --delete %s: %v\n%s", branch, err, strings.TrimSpace(string(out)))
	}
	return false, nil
}

// syncMainClone is the seam over Sync that merge calls to fast-forward the
// canonical clone's local default branch after a successful merge. A package var
// so merge tests drive it without git or the network, mirroring the gh seams.
var syncMainClone = Sync

// postMergeRetro is the seam over the post-merge retro merge runs once the PR
// has landed (tdd.PostMergeRetro): it reads the PR's journey and records any
// friction for the session that ran the merge. It never fails the merge.
var postMergeRetro = tdd.PostMergeRetro

// Merge is a resolved merge of the worktree branch's PR. It honors GitHub's own
// gates: gh refuses a PR that is not mergeable or whose required checks are
// red, so this never force-merges (no --admin). Merge does NOT touch the local
// worktree — run `prune` for that, after (or instead of) merging.
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
		fmt.Fprintf(&b, "  deletes the PR's remote branch after merging (local worktree left for prune)\n")
	}
	fmt.Fprintf(&b, "  honors GitHub's gates — a non-mergeable or red-CI PR is refused (no force)\n")
	fmt.Fprintf(&b, "  a repo declaring mutants-at-merge is judged locally first: the merge is built in a\n"+
		"  throwaway checkout and run through the pre-merge gate before the PR lands\n")
	fmt.Fprintf(&b, "\nrun again without --dry to merge (then: aphrollo workspace prune).\n")
	return b.String()
}

// Apply resolves the branch's open PR and merges it, printing the outcome. A
// branch with no PR points at `pr`; the merge itself is gh's call.
//
// Before merging it reads CI through ghCIStatus and refuses unless the state is
// exactly "green" (#385): `gh pr merge` without `--admin` does not itself
// require every check to be green — it refuses on a genuine merge conflict but
// not on a failing or still-running required check — so a shell pipeline
// wrapped around a separate watch (as the merge-on-green flow used to do) could
// lose that signal and report success anyway (a `gh pr checks --watch
// --fail-fast | grep | head` pipeline's exit status is `head`'s, not `gh`'s).
// Reading the status here, structurally, closes that gap in the merge verb
// itself rather than in a wrapper around it. "red" and "pending" refuse by
// name; "none" and a read error refuse too — an indeterminate status must
// never be treated as clear (the corroborating incident on #385 was exactly a
// broken read silently parsed as "nothing pending").
func (m *Merge) Apply(stdout, stderr io.Writer) error {
	pr, err := ghViewPR(m.Target.Worktree, m.Target.Branch)
	if err != nil {
		return err
	}
	if pr == nil {
		return fmt.Errorf("no open PR for %s — run: aphrollo workspace pr", m.Target.Branch)
	}
	body, undercoverOn, err := undercoverMerge(m.Target, m.Method)
	if err != nil {
		return fmt.Errorf("refusing to merge %s: %w", m.Target.Branch, err)
	}
	ci, ciErr := ghCIStatus(m.Target.Worktree, m.Target.Branch)
	if ciErr != nil {
		return fmt.Errorf("checking CI status for %s: %w", m.Target.Branch, ciErr)
	}
	if ci.State != "green" {
		detail := ci.State
		if ci.State == "red" && ci.Failing > 0 {
			detail = fmt.Sprintf("red (%d failing)", ci.Failing)
		}
		return fmt.Errorf("refusing to merge %s: required checks are not green (%s)", m.Target.Branch, detail)
	}
	// The local pre-merge gate, on the tree this merge is about to create —
	// before GitHub creates it. Landing through a PR makes no local merge
	// commit, so the pre-merge-commit hook never fires and everything it
	// carries (the mechanical suites, and the mutation measurement a repo
	// declaring mutants-at-merge is promised) would otherwise be skipped for
	// this path alone. It runs AFTER the two remote reads above because they
	// are cheap and this is not: a PR GitHub itself refuses never pays for a
	// local measurement.
	if err := premergeGate(m.Target, stderr); err != nil {
		return fmt.Errorf("refusing to merge %s: %w", m.Target.Branch, err)
	}
	merge := func() error { return ghMergePR(m.Target.Worktree, m.Target.Branch, m.Method) }
	if undercoverOn && m.Method != "rebase" {
		merge = func() error { return ghMergePRBody(m.Target.Worktree, m.Target.Branch, m.Method, body) }
	}
	if err := merge(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "merged PR #%d (%s): %s\n", pr.Number, m.Method, pr.URL)
	if m.DeleteBranch {
		skipped, err := ghDeleteRemoteBranch(m.Target.Worktree, m.Target.Branch)
		if err != nil {
			return err
		}
		if skipped {
			fmt.Fprintf(stdout, "  [skip] remote branch %s — already deleted\n", m.Target.Branch)
		} else {
			fmt.Fprintf(stdout, "  deleted remote branch %s (local worktree left for prune)\n", m.Target.Branch)
		}
	}

	// Best-effort: catch the canonical clone's local default branch up to the
	// freshly-merged origin/<default>. `workspace merge` is the one merge route
	// that otherwise never syncs the clone (the ticket-terminal cleanup path
	// covers operator-on-GitHub merges), so local main drifts behind on every
	// merge here. Sync resolves the main clone from MainRepo (NOT the worktree),
	// fetches, and strict-fast-forwards — idempotent and non-destructive.
	//
	// The PR is already merged by the time we get here, so a sync error (offline,
	// diverged, dirty clone) must NOT fail the merge: report it to stderr and
	// still exit 0, mirroring the agents cleanup handler's "never wedge the
	// terminal flow" stance. --dry never reaches Apply, so this only runs for real.
	if err := syncMainClone(m.Target.MainRepo, false, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "post-merge sync of %s failed (best-effort, merge already landed): %v\n", m.Target.MainRepo, err)
	}

	postMergeRetro(m.Target.MainRepo, m.Target.Worktree, m.Target.Branch, pr.Number, stderr)

	fmt.Fprintf(stdout, "  next: aphrollo workspace prune  (sweep the merged local worktree)\n")
	return nil
}
