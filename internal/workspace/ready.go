package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ghReadyPR is the seam over `gh pr ready`, a package var so tests drive ready's
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

// Ready flips the worktree branch's draft PR to ready-for-review — the git-side
// of moving a kanban card from in_progress to review. It is the inverse end of
// the draft-by-default ship: ship opens the WIP draft, ready hands it off.
// Idempotent: a PR that is already ready is reported, not re-flipped.
type Ready struct {
	Target *Target
}

// ReadyPlan resolves the flip without touching gh. It refuses a detached HEAD;
// the absence of a PR surfaces at Apply (it needs gh to know).
func ReadyPlan(t *Target) (*Ready, error) {
	if t.Branch == "HEAD" {
		return nil, fmt.Errorf("detached HEAD in %s — check out a branch before marking a PR ready", t.Worktree)
	}
	return &Ready{Target: t}, nil
}

// Render previews the flip.
func (r *Ready) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace ready: mark %s's PR ready for review  (cwd %s)\n", r.Target.Branch, r.Target.Worktree)
	if !apply {
		fmt.Fprintf(&b, "  an already-ready PR is reported, not re-flipped\n")
		fmt.Fprintf(&b, "\nrun again with --apply to flip the draft PR to ready.\n")
	}
	return b.String()
}

// Apply flips a draft PR to ready, reporting the resulting state for the relay.
// No PR points the caller at ship; an already-ready PR is a no-op.
func (r *Ready) Apply(stdout, stderr io.Writer) error {
	info, err := ghViewPR(r.Target.Worktree, r.Target.Branch)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("no PR for %s — open one first: aphrollo workspace ship", r.Target.Branch)
	}
	if !info.IsDraft {
		fmt.Fprintf(stdout, "PR #%d already ready for review: %s\n", info.Number, info.URL)
		reportPRState(stdout, info)
		return nil
	}
	if err := ghReadyPR(r.Target.Worktree, r.Target.Branch); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "PR #%d now ready for review: %s\n", info.Number, info.URL)
	// The flip just cleared the draft flag; report the resulting open state.
	info.IsDraft = false
	reportPRState(stdout, info)
	return nil
}
