package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PruneTicket is the per-ticket, per-repo counterpart to the Prune sweep: given a
// repo + branch it removes exactly that ONE ticket's worktree and is safe to
// re-run — a worktree that is already gone is reported, not an error — so a
// post-merge cleanup path can re-run on redelivery without wedging. Unlike the
// `remove` verb it leaves the local branch alone (matching the sweep, which only
// touches worktrees); deleting the branch stays `remove`'s explicit job. Like the
// sweep it folds in `git worktree prune` so no stale admin record lingers.
type PruneTicket struct {
	top      string // main clone toplevel the worktree belongs to
	worktree string // the linked worktree dir to remove
	Force    bool   // remove even a dirty worktree
}

// PruneTicketPlan resolves repo+branch to a worktree path without executing,
// refusing to target the worktree the caller is standing in (git's own error for
// that is cryptic — surface a clear one first, matching RemovePlan's guard).
func PruneTicketPlan(repo, branch, into string) (*PruneTicket, error) {
	if repo == "" {
		return nil, fmt.Errorf("repo path required")
	}
	slug, err := Slugify(branch)
	if err != nil {
		return nil, err
	}
	top, err := resolveMainRepo(repo)
	if err != nil {
		return nil, err
	}
	base := into
	if base == "" {
		base = DefaultWorktreeBase(top)
	}
	wt := filepath.Join(base, slug)
	if cwd, err := os.Getwd(); err == nil && pathWithin(cwd, wt) {
		return nil, fmt.Errorf("refusing to prune the worktree you're standing in — cd out first:\n  cd %s && aphrollo workspace prune %s %s", top, repo, branch)
	}
	return &PruneTicket{top: top, worktree: wt}, nil
}

// Run removes the ticket's worktree idempotently. apply=false previews. A
// worktree that no longer exists is reported "already gone" with no error — the
// idempotency guarantee — and still folds in `git worktree prune` so a stale
// admin record left by an out-of-band removal is swept on that path too. git's
// own "is not a working tree" message is treated the same way.
func (p *PruneTicket) Run(apply bool, stdout, stderr io.Writer) error {
	_, statErr := os.Stat(p.worktree)
	gone := os.IsNotExist(statErr)

	if !apply {
		if gone {
			fmt.Fprintf(stdout, "already gone: %s\n", p.worktree)
		} else {
			fmt.Fprintf(stdout, "would prune: %s\nrun again without --dry to execute.\n", p.worktree)
		}
		return nil
	}

	if gone {
		// Idempotent: nothing to remove. Sweep any stale admin record git may still
		// list for the now-absent dir.
		_ = exec.Command("git", "-C", p.top, "worktree", "prune").Run()
		fmt.Fprintf(stdout, "already gone: %s\n", p.worktree)
		return nil
	}

	if err := removeWorktree(p.top, p.worktree, p.Force); err != nil {
		if isNotAWorktree(err.Error()) {
			fmt.Fprintf(stdout, "already gone: %s\n", p.worktree)
			return nil
		}
		return err
	}
	fmt.Fprintf(stdout, "pruned: %s\n", p.worktree)
	return nil
}

// Prune sweeps a repo's worktrees and removes the ones whose work is finished —
// a worktree is removed ONLY when ALL hold: its PR is MERGED, the tree is CLEAN
// (no uncommitted changes), and it is not the worktree the caller is standing
// in. Anything else is SKIPPED with a reason (open PR / no PR / dirty / current)
// so the sweep never yanks live work. After removing the merged trees it folds
// in `git worktree prune` to drop any stale admin records left behind.
type Prune struct {
	Repo  string // repo toplevel whose worktrees are swept
	Force bool   // remove a MERGED worktree even when it has uncommitted changes
}

// PrunePlan resolves the repo (cwd when repoArg is "") without executing.
func PrunePlan(repoArg string) (*Prune, error) {
	path := repoArg
	if path == "" {
		path = "."
	}
	top, err := resolveMainRepo(path)
	if err != nil {
		return nil, err
	}
	return &Prune{Repo: top}, nil
}

// ghPRState is the seam over `gh pr view <branch> --json state` — a package var
// so the sweep tests run without gh or the network. It returns the PR's state
// ("MERGED"/"OPEN"/"CLOSED") or "" when the branch genuinely has no PR (absence,
// not an error). A real gh failure (network/auth/gh-missing) returns a non-nil
// error so the sweep skips the worktree rather than misreading the failure as
// "no PR". The real implementation shells gh in the worktree, where gh resolves
// the repo from origin.
var ghPRState = func(wt, branch string) (string, error) {
	cmd := exec.Command("gh", "pr", "view", "--json", "state", "-q", ".state", "--", branch)
	cmd.Dir = wt
	out, err := cmd.CombinedOutput()
	if err != nil {
		// gh exits non-zero both for "no PR for this branch" and for genuine
		// failures. Only the former is an absence; distinguish on gh's message
		// and propagate everything else so the sweep skips (never prunes) on a
		// transient gh error.
		if isNoPRError(string(out)) {
			return "", nil
		}
		return "", fmt.Errorf("gh pr view %s: %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// isNoPRError reports whether gh's output is the benign "this branch has no open
// PR" message (an absence) rather than a real failure (auth/network/gh-missing).
func isNoPRError(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "no pull requests found") ||
		strings.Contains(o, "no open pull requests found") ||
		strings.Contains(o, "no pull request found") ||
		strings.Contains(o, "no pr")
}

// worktreeEntry is one linked worktree the sweep considers: its path and the
// branch checked out there.
type worktreeEntry struct {
	Path   string
	Branch string
}

// pruneDecision is the resolved verdict for one worktree: remove it, or skip it
// with a reason.
type pruneDecision struct {
	wt     worktreeEntry
	remove bool
	reason string // "PR merged" when removing; the skip reason otherwise
}

// Run sweeps the repo's worktrees and removes the merged-and-clean ones.
// apply=false lists what WOULD be pruned and skipped without mutating; apply=true
// removes them and folds in a `git worktree prune` of stale admin records. Either
// way it prints a parseable per-worktree receipt plus a tally.
func (p *Prune) Run(apply bool, stdout, stderr io.Writer) error {
	entries, err := linkedWorktrees(p.Repo)
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()

	pruned := 0
	failed := 0
	for _, e := range entries {
		d := p.decide(e, cwd)
		if !d.remove {
			fmt.Fprintf(stdout, "skip: %s (%s)\n", e.Path, d.reason)
			continue
		}
		if !apply {
			fmt.Fprintf(stdout, "would prune: %s (%s)\n", e.Path, d.reason)
			pruned++
			continue
		}
		if err := removeWorktree(p.Repo, e.Path, p.Force); err != nil {
			fmt.Fprintf(stderr, "could not remove %s: %v\n", e.Path, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "pruned: %s (%s)\n", e.Path, d.reason)
		pruned++
	}

	verb := "would prune"
	if apply {
		verb = "pruned"
		// Fold in the admin-record prune so any stale entries (worktrees whose
		// dirs are already gone) are swept in the same call.
		_ = exec.Command("git", "-C", p.Repo, "worktree", "prune").Run()
	}
	// Surface removal failures in the tally so a permission-failed removal is not
	// hidden behind a clean-looking count.
	if failed > 0 {
		fmt.Fprintf(stdout, "%s %d, failed %d worktree(s)\n", verb, pruned, failed)
	} else {
		fmt.Fprintf(stdout, "%s %d worktree(s)\n", verb, pruned)
	}
	return nil
}

// decide resolves the verdict for one worktree: remove it only when its PR is
// MERGED, the tree is clean (or --force), and it is not the cwd worktree.
func (p *Prune) decide(e worktreeEntry, cwd string) pruneDecision {
	if cwd != "" && pathWithin(cwd, e.Path) {
		return pruneDecision{wt: e, reason: "current"}
	}
	state, err := ghPRState(e.Path, e.Branch)
	if err != nil {
		// A gh failure is fail-safe: skip the worktree, never prune on a state we
		// could not read.
		return pruneDecision{wt: e, reason: "could not check PR state (gh unavailable)"}
	}
	switch strings.ToUpper(state) {
	case "MERGED":
		// merged — fall through to the clean check below.
	case "OPEN":
		return pruneDecision{wt: e, reason: "open PR"}
	case "CLOSED":
		return pruneDecision{wt: e, reason: "closed PR"}
	default: // ""
		return pruneDecision{wt: e, reason: "no PR"}
	}
	if !p.Force && !worktreeClean(e.Path) {
		return pruneDecision{wt: e, reason: "dirty"}
	}
	return pruneDecision{wt: e, remove: true, reason: "PR merged"}
}

// linkedWorktrees lists the repo's LINKED worktrees (the main clone, listed
// first by git, is excluded — it is never pruned). Each entry carries the
// worktree path and the branch checked out there; a detached worktree reports
// "HEAD".
func linkedWorktrees(repo string) ([]worktreeEntry, error) {
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.Path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			cur.Branch = "HEAD"
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	flush()
	// git lists the main worktree first; drop it — the sweep only touches linked
	// worktrees, never the canonical clone.
	if len(entries) > 0 {
		entries = entries[1:]
	}
	return entries, nil
}

// worktreeClean reports whether the worktree has no uncommitted changes —
// `git status --porcelain` empty.
func worktreeClean(wt string) bool {
	out, err := exec.Command("git", "-C", wt, "status", "--porcelain").Output()
	if err != nil {
		return false // can't tell => treat as dirty, don't remove
	}
	return len(strings.TrimSpace(string(out))) == 0
}

// removeWorktree runs `git worktree remove` (with --force when requested),
// followed by `git worktree prune` so the admin record never lingers.
func removeWorktree(repo, wt string, force bool) error {
	args := []string{"-C", repo, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wt)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("git", "-C", repo, "worktree", "prune").Run()
	return nil
}
