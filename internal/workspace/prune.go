package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gatePRMergeHolderFile matches PRGateHolderFile
// (internal/tdd/merge/premergepr.go): the record GatePRMerge writes into its
// own throwaway checkout the instant it builds it, naming the pid that owns
// it. It is the ONLY liveness evidence this sweep trusts — a checkout with no
// record predates the record (an older binary's leak, or something else
// entirely) and is left alone rather than guessed at.
const gatePRMergeHolderFile = ".aphrollo-prmerge-holder"

// gatePRMergePrefix names every throwaway checkout GatePRMerge builds
// (internal/tdd/merge, prGateMergedCheckout's os.MkdirTemp pattern).
const gatePRMergePrefix = "gate-prmerge-"

// isGatePRMergeWorktree reports whether path is one of GatePRMerge's own
// throwaway checkouts rather than an operator's lane.
func isGatePRMergeWorktree(path string) bool {
	base := filepath.Base(path)
	return strings.HasPrefix(base, gatePRMergePrefix)
}

// decideGatePRMergeWorktree judges a throwaway GatePRMerge checkout on its
// own terms: it is never a lane and carries no PR, so the merged-PR rule's
// "no PR" reading is simply wrong for it — GatePRMerge (internal/tdd/merge)
// already judged the tree itself, and the checkout exists only for as long
// as that judgment takes. Its holder record is the only evidence this
// decides on: no record, or a still-running pid, means "leave it"; a dead
// one means the gate that built it never got to remove it and it is safe to.
func decideGatePRMergeWorktree(e worktreeEntry) pruneDecision {
	pid, ok := gatePRMergeHolderPID(e.Path)
	if !ok {
		return pruneDecision{wt: e, reason: "merge-gate checkout, no holder record"}
	}
	if gatePRMergeHolderAlive(pid) {
		return pruneDecision{wt: e, reason: fmt.Sprintf("merge-gate checkout in use (pid %d)", pid)}
	}
	return pruneDecision{wt: e, remove: true, reason: fmt.Sprintf("merge-gate checkout, holder pid %d is gone", pid)}
}

// gatePRMergeHolderPID reads the pid line of wt's holder record. ok=false
// covers "no record", "unreadable" and "malformed" alike — every one of
// those means "unknown", never "dead".
func gatePRMergeHolderPID(wt string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(wt, gatePRMergeHolderFile))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "pid=")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// PruneTicket is the per-ticket, per-repo counterpart to the Prune sweep: given a
// repo + branch it removes exactly that ONE ticket's worktree and is safe to
// re-run — a worktree that is already gone is reported, not an error — so a
// post-merge cleanup path can re-run on redelivery without wedging. Unlike the
// `remove` verb it leaves the local branch alone (matching the sweep, which only
// touches worktrees); deleting the branch stays `remove`'s explicit job. Like the
// sweep it touches the admin entry of that one worktree and no other.
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
// idempotency guarantee — and still drops that worktree's own admin entry, so a
// record left by an out-of-band removal is cleared on that path too. git's own
// "is not a working tree" message is treated the same way.
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
		// Idempotent: nothing to remove. Drop the admin entry git may still keep
		// for this one now-absent dir.
		dropMissingWorktree(p.top, p.worktree)
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
// so the sweep never yanks live work. It touches no admin entry of a worktree
// it did not remove: a directory that looks missing may only be invisible to
// this process (a systemd PrivateTmp view of the host's /tmp), and its
// registration belongs to whoever is still working there.
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
	out, err := ghCombinedOutput(wt, "pr", "view", "--json", "state", "-q", ".state", "--", branch)
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

// ghPRHeadOid is the seam over `gh pr view <branch> --json headRefOid` — a
// package var so the sweep tests run without gh or the network. It returns the
// SHA gh last recorded for the branch's head (what the PR actually merged),
// which stays fixed once the PR is merged unless the branch is pushed again.
// Comparing it against the worktree's actual HEAD is what lets decide() tell a
// merged-and-untouched worktree apart from one a builder kept committing to
// after the merge — see #163: `git status --porcelain` alone cannot make that
// distinction, since new commits leave the tree clean again.
var ghPRHeadOid = func(wt, branch string) (string, error) {
	out, err := ghCombinedOutput(wt, "pr", "view", "--json", "headRefOid", "-q", ".headRefOid", "--", branch)
	if err != nil {
		return "", fmt.Errorf("gh pr view %s: %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// localHeadSHA returns the worktree's current HEAD commit.
func localHeadSHA(wt string) (string, error) {
	out, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
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
// removes them. Either way it prints a parseable per-worktree receipt plus a
// tally.
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
		// A gate-prmerge checkout is a merged --no-commit tree by construction
		// — dirty by design, never something a plain `git worktree remove`
		// would accept — so removing one always forces, regardless of --force.
		force := p.Force || isGatePRMergeWorktree(e.Path)
		if err := removeWorktree(p.Repo, e.Path, force); err != nil {
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
	if isGatePRMergeWorktree(e.Path) {
		return decideGatePRMergeWorktree(e)
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
	// A clean tree is not enough: a builder who reuses a merged branch's
	// worktree for follow-up work commits it, so `git status --porcelain`
	// goes clean again with the new work still sitting there (#163). Compare
	// HEAD against what gh actually recorded as merged (headRefOid) and skip
	// rather than prune when they disagree.
	mergedHead, err := ghPRHeadOid(e.Path, e.Branch)
	if err != nil {
		return pruneDecision{wt: e, reason: "could not check PR state (gh unavailable)"}
	}
	if mergedHead != "" {
		head, err := localHeadSHA(e.Path)
		if err != nil {
			return pruneDecision{wt: e, reason: "could not check PR state (gh unavailable)"}
		}
		if head != mergedHead {
			return pruneDecision{wt: e, reason: "local commits beyond the merged PR"}
		}
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
	return excludeMainClone(parseWorktreeList(string(out)), repo), nil
}

// parseWorktreeList parses `git worktree list --porcelain` output into every
// entry it lists, in the order git printed them — including the main clone.
// It makes no positional assumption; callers decide how to treat any entry.
func parseWorktreeList(out string) []worktreeEntry {
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.Path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			// Same MSYS forward-slash normalization as gitToplevel: Git for
			// Windows always prints porcelain paths with "/", so Clean folds
			// them to the native separator before any caller compares or
			// joins against a filepath.Join-built path.
			cur.Path = filepath.Clean(strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))
			cur.Branch = "HEAD"
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	flush()
	return entries
}

// excludeMainClone drops the entry whose path IS repo (the main checkout,
// never swept) by comparing PATHS, never by list position — `git worktree
// list --porcelain` lists the main worktree first in practice, but nothing
// enforces that ordering, and internal/tdd's mergePruneWorktrees (the
// identical rule for the identical operation) already deliberately rejects
// trusting it (see #172).
func excludeMainClone(entries []worktreeEntry, repo string) []worktreeEntry {
	var linked []worktreeEntry
	for _, e := range entries {
		if samePath(e.Path, repo) {
			continue
		}
		linked = append(linked, e)
	}
	return linked
}

// samePath reports whether two worktree paths name the same directory,
// normalizing separators and case — git may report a path with either
// separator, and the same directory routinely appears under two drive-letter
// or short/long-name spellings on Windows.
func samePath(a, b string) bool {
	clean := func(p string) string {
		if p == "" {
			return ""
		}
		return strings.ToLower(strings.TrimRight(filepath.ToSlash(filepath.Clean(p)), "/"))
	}
	return clean(a) == clean(b)
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

// removeWorktree runs `git worktree remove` (with --force when requested). A
// successful remove has already deleted that worktree's admin entry; it never
// follows up with `git worktree prune`, which would also delete the entry of
// every OTHER worktree whose directory this process cannot see.
func removeWorktree(repo, wt string, force bool) error {
	args := []string{"-C", repo, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wt)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// dropMissingWorktree deletes the admin entry of the one linked worktree at wt,
// whose directory is already gone. `git worktree remove` of a missing directory
// drops exactly that entry; an entry git does not know, or a locked one, is left
// as it is.
func dropMissingWorktree(repo, wt string) {
	_ = exec.Command("git", "-C", repo, "worktree", "remove", wt).Run()
}
