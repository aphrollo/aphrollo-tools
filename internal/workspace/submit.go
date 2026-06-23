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

// Submit is the CI-guarded handoff that moves a card in_progress -> review. It
// pushes (idempotent), reads the branch PR's CI state INSIDE the verb (the only
// gh check-state read, so the caller needs no extra call), and ONLY on green
// flips the draft PR to ready and sets the PR body to the summary. On red or
// pending it does NOT flip, prints a blocked/held receipt, and exits non-zero —
// re-callable, idempotent. It replaces the old `ready` verb (kept as a hidden
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
	fmt.Fprintf(&b, "workspace submit: %s -> review (CI-guarded)  (cwd %s)\n", s.Target.Branch, s.Target.Worktree)
	if !apply {
		fmt.Fprintf(&b, "  push (idempotent), then flip the draft PR to in-review ONLY if CI is green\n")
		fmt.Fprintf(&b, "  CI red or pending: not flipped, exits non-zero, re-callable\n")
		fmt.Fprintf(&b, "\nrun again without --dry to submit.\n")
	}
	return b.String()
}

// Apply pushes, gates on CI, and on green flips the PR ready + sets its body.
// The receipt is stateful so no follow-up gh call is needed.
func (s *Submit) Apply(stdout, stderr io.Writer) error {
	wt, branch := s.Target.Worktree, s.Target.Branch
	// Push is idempotent — a re-driven submit re-pushes a no-op and reuses the PR.
	if err := s.push.Apply(stdout, stderr); err != nil {
		return err
	}

	info, err := ghViewPR(wt, branch)
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
	switch ci.State {
	case "red":
		fmt.Fprintf(stdout, "blocked: CI red (%d failing), NOT marked ready\n", ci.Failing)
		fmt.Fprintf(stdout, "  PR #%d %s  (re-run submit when CI is green)\n", info.Number, info.URL)
		return fmt.Errorf("CI red — %d failing check(s); not submitted", ci.Failing)
	case "green":
		// fall through to the handoff
	default: // pending | none
		fmt.Fprintf(stdout, "held: CI %s, NOT marked ready yet\n", ci.State)
		fmt.Fprintf(stdout, "  PR #%d %s  (re-run submit when CI is green)\n", info.Number, info.URL)
		return fmt.Errorf("CI %s — not submitted yet", ci.State)
	}

	// Green: set the PR body to the summary, then flip the draft to ready.
	if strings.TrimSpace(s.Summary) != "" {
		if err := ghEditPRBody(wt, branch, s.Summary); err != nil {
			return err
		}
	}
	if info.IsDraft {
		if err := ghReadyPR(wt, branch); err != nil {
			return err
		}
		info.IsDraft = false
	}

	// Stateful receipt: never surfaces the literal ready_for_review.
	fmt.Fprintf(stdout, "submitted PR #%d  draft -> in review\n", info.Number)
	if newN := s.push.ahead; newN != "" && newN != "0" {
		fmt.Fprintf(stdout, "  pushed in sync (%s new)\n", newN)
	} else {
		fmt.Fprintf(stdout, "  pushed in sync\n")
	}
	fmt.Fprintf(stdout, "  ci green\n")
	fmt.Fprintf(stdout, "  handoff in_progress -> review\n")
	fmt.Fprintf(stdout, "  %s\n", info.URL)
	reportPRState(stdout, info)
	return nil
}
