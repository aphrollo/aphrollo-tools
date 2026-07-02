package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ghReadyPR is the seam over `gh pr ready`, a package var so tests drive submit's
// flip logic without gh or the network. The real implementation marks the
// branch's draft PR as ready-for-review in the worktree's repo.
var ghReadyPR = func(wt, branch string) error {
	cmd := exec.Command("gh", "pr", "ready", "--", branch)
	cmd.Dir = wt
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr ready: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ghEditPRBody is the seam over `gh pr edit --body`, set so the submit summary
// lands on the PR. A package var so tests observe the body without gh.
var ghEditPRBody = func(wt, branch, body string) error {
	cmd := exec.Command("gh", "pr", "edit", branch, "--body", body)
	cmd.Dir = wt
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr edit --body: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Submit is the coder's ONE-SHOT handoff that moves a card in_progress -> review.
// It pushes (idempotent), reads the branch PR's CI state INSIDE the verb (the only
// gh check-state read, so the caller needs no extra call), and UNCONDITIONALLY
// flips the draft PR to ready + sets the PR body to the summary. The flip is the
// handoff and the coder signals it exactly once: it happens on green, pending,
// red, or a merge conflict alike. Any remaining problem is repaired by a follow-up
// `push` to the same PR (which stays ready) — never a re-submit. The server-side
// AllOpenGreen gate (non-draft + ci_state=success) is what holds the reviewer
// until CI is actually green, so flipping early never arms review prematurely;
// holding the draft here instead stranded tickets, since the coder's turn ends
// before it can re-run submit. Only a real failure (the gh flip call itself) is
// fatal. Re-callable, idempotent. It replaces the old `ready` verb (kept as a
// hidden alias).
type Submit struct {
	Target  *Target
	Summary string
	push    *Push
}

// SubmitPlan resolves the push stage and snapshots the request. It refuses a
// detached HEAD; the CI/PR state surfaces at Apply (it needs gh to know).
func SubmitPlan(t *Target, summary string) (*Submit, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before submitting", t.Worktree)
	}
	p, err := PushPlan(t, false)
	if err != nil {
		return nil, err
	}
	return &Submit{Target: t, Summary: summary, push: p}, nil
}

// Render previews the submit. The dry-run is explicit that the flip is gated on
// green CI and won't run here.
func (s *Submit) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace submit: %s -> review  (cwd %s)\n", s.Target.Branch, s.Target.Worktree)
	if !apply {
		fmt.Fprintf(&b, "  push (idempotent), then flip the draft PR to in-review (the one-shot handoff)\n")
		fmt.Fprintf(&b, "  flips on any CI state — review arms server-side once CI is green\n")
		fmt.Fprintf(&b, "  red CI / merge conflict: still flipped, fix via a follow-up push (no re-submit)\n")
		fmt.Fprintf(&b, "\nrun again without --dry to submit.\n")
	}
	return b.String()
}

// Apply pushes, gates on CI, and on green flips the PR ready + sets its body.
// The receipt is stateful so no follow-up gh call is needed.
func (s *Submit) Apply(stdout, stderr io.Writer) error {
	wt, branch := s.Target.Worktree, s.Target.Branch
	// How many commits this push delivers, captured BEFORE the push (after it the
	// remote ref equals HEAD). push.ahead is set when an upstream/remote branch
	// exists; for a brand-new branch it is "" — count against the default base.
	newCommits := s.push.ahead
	if newCommits == "" {
		newCommits = aheadCount(wt, "origin/"+resolveDefaultBranch(wt))
	}

	// Push is idempotent — a re-driven submit re-pushes a no-op and reuses the PR.
	// Run it QUIETLY (its own receipt is suppressed) so submit emits exactly one
	// consolidated receipt with no duplicate pr-url:/ci lines.
	if err := s.push.Apply(io.Discard, stderr); err != nil {
		return err
	}

	// Read the PR, re-polling past GitHub's async UNKNOWN window so a conflicted
	// branch is caught here while the coder is still live.
	info, err := viewPRMergeable(wt, branch)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("no PR for %s — run: aphrollo workspace push", branch)
	}

	ci, err := ghCIStatus(wt, branch)
	if err != nil {
		return err
	}
	conflict := isConflicting(info)
	// Mergeability never resolved within the bound — say so rather than imply a
	// clean branch. Non-blocking; a follow-up push re-resolves it.
	if mergeUnknown(info) {
		fmt.Fprintf(stdout, "mergeable: unknown — push to recheck\n")
	}

	// The handoff is ONE-SHOT and unconditional: flip the draft ready now. The coder
	// signals "done" exactly once; ANY remaining problem — red CI, a merge conflict,
	// or CI still pending — is repaired by a follow-up `push` to the SAME PR, which
	// stays ready, with no re-submit. Holding the draft here instead stranded
	// tickets: the coder ends its turn before it can re-run submit. The server-side
	// AllOpenGreen gate (non-draft + ci_state success) holds the reviewer until CI is
	// actually green, and a conflicted branch can't green, so flipping early never
	// arms review prematurely. Only a real failure (the gh flip call itself) is fatal.
	flipped := false
	if info.IsDraft {
		if err := ghReadyPR(wt, branch); err != nil {
			return err
		}
		info.IsDraft = false
		flipped = true
	}

	// Body is best-effort cosmetic: a failure after a good flip must NOT fail the
	// submit — the PR is already in review. Warn and carry on.
	if strings.TrimSpace(s.Summary) != "" {
		if err := ghEditPRBody(wt, branch, s.Summary); err != nil {
			fmt.Fprintf(stdout, "warning: PR body not updated (%v) — handoff still succeeded\n", err)
		}
	}

	// Stateful receipt: never surfaces the literal ready_for_review. A re-run on an
	// already-ready PR is the idempotent [skip] path (no re-flip).
	if flipped {
		fmt.Fprintf(stdout, "submitted PR #%d  draft -> in review\n", info.Number)
	} else {
		fmt.Fprintf(stdout, "already in review [skip]  PR #%d %s\n", info.Number, info.URL)
	}
	s.receiptTail(stdout, info, newCommits, ci, conflict)
	// Raise the first-class readiness signal at the api's internal submit endpoint
	// on BOTH paths. On the flip path the ready_for_review webhook is redundant
	// defense; on the [skip] path (an already-ready PR that never re-drafts) NO
	// webhook fires, so this is the ONLY thing that re-arms review after a bounce.
	// Best-effort — a failure warns but never fails the handoff.
	raiseSubmitSignal(stdout, !flipped)
	return nil
}

// receiptTail prints the shared post-state lines for both the flipped and the
// already-ready paths: sync state, the CI/conflict guidance, handoff, url, and
// the relay pr-* lines. The guidance is honest about what still blocks review:
// a conflict (CI can't run) says rebase+push; red says push a fix; pending says
// review arms when green. The server-side AllOpenGreen gate, not this verb, is
// what actually holds the reviewer.
func (s *Submit) receiptTail(stdout io.Writer, info *PRInfo, newCommits string, ci CIStatus, conflict bool) {
	if newCommits != "" && newCommits != "0" {
		fmt.Fprintf(stdout, "  pushed %s new commit(s)\n", newCommits)
	} else {
		fmt.Fprintf(stdout, "  already in sync\n")
	}
	switch {
	case conflict:
		fmt.Fprintf(stdout, "  merge conflict — rebase onto %s + push (review arms when green)\n", resolveDefaultBranch(s.Target.Worktree))
	case ci.State == "green":
		fmt.Fprintf(stdout, "  ci green\n")
	case ci.State == "red":
		fmt.Fprintf(stdout, "  ci red (%d failing) — push a fix (review arms when green)\n", ci.Failing)
	default: // pending | none
		fmt.Fprintf(stdout, "  ci %s — review arms when green\n", ci.State)
	}
	fmt.Fprintf(stdout, "  handoff in_progress -> review\n")
	fmt.Fprintf(stdout, "  %s\n", info.URL)
	reportPRState(stdout, info)
}
