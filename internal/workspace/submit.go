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

// Submit is the one-shot handoff that moves a card in_progress -> review. It
// pushes (idempotent), reads the branch PR's CI state INSIDE the verb (the only
// gh check-state read, so the caller needs no extra call), and flips the draft PR
// to ready + sets the PR body to the summary. The flip is the handoff: it happens
// on green AND while CI is still pending, because the server-side AllOpenGreen
// gate (ci_state=success) is what actually holds review until CI passes — holding
// the draft here instead stranded tickets, since the coder's turn ends before it
// can re-run submit once CI greens. Only RED CI (or a merge conflict) blocks the
// flip: known-broken work is not handed off while the coder is live to fix it.
// Re-callable, idempotent. It replaces the old `ready` verb (kept as a hidden
// alias).
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
		fmt.Fprintf(&b, "  push (idempotent), then flip the draft PR to in-review (the handoff)\n")
		fmt.Fprintf(&b, "  CI still pending is fine — review arms server-side once CI is green\n")
		fmt.Fprintf(&b, "  CI red or a merge conflict: not flipped, exits non-zero, re-callable\n")
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

	// Hard-block a conflicted branch BEFORE the CI gate. A conflicted branch can't
	// compute a merge ref, so its required checks queue forever and the CI gate
	// would otherwise report a misleading "CI pending" — surface the real cause
	// and the concrete fix instead, and do NOT flip the draft.
	if isConflicting(info) {
		base := resolveDefaultBranch(wt)
		fmt.Fprintf(stdout, "blocked: branch has merge conflicts, NOT marked ready\n")
		fmt.Fprintf(stdout, "  PR #%d %s\n", info.Number, info.URL)
		fmt.Fprintf(stdout, "  fix: rebase onto %s and resolve, then re-run submit\n", base)
		return fmt.Errorf("branch has merge conflicts — not submitted")
	}

	ci, err := ghCIStatus(wt, branch)
	if err != nil {
		return err
	}
	// Mergeability never resolved within the bound — say so rather than imply a
	// clean branch. Non-blocking: the CI gate below still governs the flip, and a
	// re-run re-polls.
	if mergeUnknown(info) {
		fmt.Fprintf(stdout, "mergeable: unknown — re-run to recheck\n")
	}
	// Red is the ONLY state that blocks the handoff: known-broken work must not be
	// handed off, and the coder is still live to push a fix. Pending/none/green all
	// hand off — the flip is the meaningful one-shot handoff. Holding the draft on
	// PENDING was the strand bug: the coder ends its turn before it can re-run submit
	// when CI greens, leaving the PR draft forever. The platform's server-side
	// AllOpenGreen gate (ci_state=success) holds review until CI is actually green,
	// so a flip-while-pending never arms review prematurely.
	if ci.State == "red" {
		fmt.Fprintf(stdout, "blocked: CI red (%d failing), NOT marked ready\n", ci.Failing)
		fmt.Fprintf(stdout, "  PR #%d %s  (push a fix — CI must be green before review arms)\n", info.Number, info.URL)
		return fmt.Errorf("CI red — %d failing check(s); not submitted", ci.Failing)
	}

	// If the PR is already ready, this is an idempotent re-run: report the honest
	// [skip] receipt without re-flipping or touching the body.
	if !info.IsDraft {
		fmt.Fprintf(stdout, "already in review [skip]  PR #%d %s\n", info.Number, info.URL)
		s.receiptTail(stdout, info, newCommits, ci.State)
		return nil
	}

	// Flip the draft to ready FIRST — that is the meaningful handoff. If the flip
	// itself fails, return the error without touching the body.
	if err := ghReadyPR(wt, branch); err != nil {
		return err
	}
	info.IsDraft = false

	// Body is best-effort cosmetic: a failure after a good flip must NOT fail the
	// submit — the PR is already in review. Warn and carry on.
	if strings.TrimSpace(s.Summary) != "" {
		if err := ghEditPRBody(wt, branch, s.Summary); err != nil {
			fmt.Fprintf(stdout, "warning: PR body not updated (%v) — handoff still succeeded\n", err)
		}
	}

	// Stateful receipt: never surfaces the literal ready_for_review.
	fmt.Fprintf(stdout, "submitted PR #%d  draft -> in review\n", info.Number)
	s.receiptTail(stdout, info, newCommits, ci.State)
	return nil
}

// receiptTail prints the shared post-state lines for both the flipped and the
// already-ready paths: sync state, ci, handoff, url, and the relay pr-* lines.
// ciState is honest about whether CI has actually passed: a handoff while CI is
// still pending says so and notes review arms once it greens (the server-side
// AllOpenGreen gate, not this verb, is what holds review).
func (s *Submit) receiptTail(stdout io.Writer, info *PRInfo, newCommits, ciState string) {
	if newCommits != "" && newCommits != "0" {
		fmt.Fprintf(stdout, "  pushed %s new commit(s)\n", newCommits)
	} else {
		fmt.Fprintf(stdout, "  already in sync\n")
	}
	if ciState == "green" {
		fmt.Fprintf(stdout, "  ci green\n")
	} else {
		fmt.Fprintf(stdout, "  ci %s — review arms when green\n", ciState)
	}
	fmt.Fprintf(stdout, "  handoff in_progress -> review\n")
	fmt.Fprintf(stdout, "  %s\n", info.URL)
	reportPRState(stdout, info)
}
