package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Sync brings a base clone's LOCAL default branch up to the remote tip after a
// PR merges. `workspace create`/`prepare` cut fresh worktrees from
// origin/<default> (post-fetch), but the canonical clone's own checked-out
// default branch never refreshes — it drifts further behind on every merge. Sync
// is the non-destructive "catch the clone up to origin" primitive the post-merge
// cleanup path calls.
//
// Best-effort, NON-DESTRUCTIVE, idempotent:
//
//   - git fetch origin — refresh the remote-tracking refs. Offline / remote-less
//     is a non-fatal warning, not an error: refreshing origin/<default> is the
//     minimum win and a clone with nothing to fetch toward is still a success.
//   - Fast-forward the LOCAL default branch (resolved, never hardcoded "main")
//     to origin/<default> ONLY when it is a strict fast-forward:
//   - HEAD is the default branch => merge --ff-only. A dirty worktree is NOT
//     refused up front: git itself refuses a fast-forward only when it would
//     overwrite an uncommitted change to a path the incoming commits touch, so
//     sync just asks git and reports git's own reason — an unrelated dirty
//     file never blocks a fast-forward it doesn't conflict with.
//   - the default branch is NOT the checked-out one => advance its ref with
//     update-ref (no checkout, so a sibling worktree's files are untouched).
//   - NEVER reset --hard, NEVER force, NEVER move the branch when it has
//     diverged (local commits ahead) — leave it, say why.
//
// Only the default branch is synced; ticket branches and other worktrees are
// left alone. Re-running on an already-current clone is a no-op success. --dry
// previews the fast-forward (the fetch still runs — it only touches
// remote-tracking refs) and mutates nothing else.
func Sync(repoArg string, dry bool, stdout, stderr io.Writer) error {
	top, err := resolveMainRepo(repoArg)
	if err != nil {
		return err
	}

	// Refresh origin so origin/<default> is the live tip. Fetch touches only
	// remote-tracking refs, never HEAD or the working tree, so it is safe even in
	// --dry. An offline / remote-less repo can't refresh — warn, don't crash.
	if out, err := gitNetworkOutput(top, "fetch", "origin", "--quiet"); err != nil {
		fmt.Fprintf(stderr, "git fetch origin: %v\n%s\n", err, strings.TrimSpace(string(out)))
	}

	def := resolveDefaultBranch(top)
	remote := "origin/" + def
	local := "refs/heads/" + def

	// Nothing to fast-forward toward (offline, or no remote default ref) — the
	// fetch was the only possible win. A clean skip, not a failure.
	if !gitRefExists(top, remote) {
		fmt.Fprintf(stdout, "%s not found — nothing to sync toward (offline or no remote) [skip]\n", remote)
		return nil
	}
	if !gitRefExists(top, local) {
		fmt.Fprintf(stdout, "no local %s branch — nothing to sync [skip]\n", def)
		return nil
	}

	// How far the local default branch trails origin's tip.
	behind, ok := gitBehindCount(top, local, remote)
	if !ok {
		return fmt.Errorf("could not compare %s with %s — fetch origin or check the default branch", def, remote)
	}
	if behind == 0 {
		fmt.Fprintf(stdout, "%s already current with %s [skip]\n", def, remote)
		return nil
	}

	// A move is only safe if it is a STRICT fast-forward: the local default branch
	// must be an ancestor of origin's tip. Local commits ahead / a divergence =>
	// refuse (never reset, never force).
	if !isAncestor(top, local, remote) {
		fmt.Fprintf(stdout, "%s has diverged from %s (local commits not on origin) — leaving it untouched\n", def, remote)
		return nil
	}

	onDefault := currentBranch(top) == def

	if dry {
		fmt.Fprintf(stdout, "would fast-forward %s to %s (%d commit(s) behind)\n", def, remote, behind)
		return nil
	}

	if onDefault {
		// On the default branch: --ff-only advances HEAD + ref together. It also
		// refuses anything that isn't a fast-forward — including a dirty tracked
		// path the incoming commits would overwrite — so git is the judge of
		// "safe to move", not a pre-check here. A refusal is reported, never
		// treated as an error: it is non-destructive, and the reason is git's own.
		if out, err := exec.Command("git", "-C", top, "merge", "--ff-only", remote).CombinedOutput(); err != nil {
			if !isDirtyPathRefusal(out) {
				fmt.Fprint(stderr, string(out))
				return fmt.Errorf("git merge --ff-only %s: %w", remote, err)
			}
			fmt.Fprintf(stdout, "%s could not fast-forward: %s\n", def, reasonLine(out))
			return nil
		}
		fmt.Fprintf(stdout, "fast-forwarded %s to %s (%d commit(s))\n", def, remote, behind)
		return nil
	}

	// The default branch is NOT checked out here: advance its ref directly.
	// update-ref moves only the branch pointer, never a working tree, and we have
	// already proven a strict fast-forward — so a sibling worktree currently on
	// the default branch keeps its files untouched.
	if out, err := exec.Command("git", "-C", top, "update-ref", local, remote).CombinedOutput(); err != nil {
		fmt.Fprint(stderr, string(out))
		return fmt.Errorf("git update-ref %s %s: %w", local, remote, err)
	}
	fmt.Fprintf(stdout, "fast-forwarded %s ref to %s (%d commit(s))\n", def, remote, behind)
	return nil
}

// isAncestor reports whether ancestor is reachable from descendant — i.e. moving
// from ancestor to descendant is a strict fast-forward (no rewrite, no merge).
func isAncestor(repo, ancestor, descendant string) bool {
	return exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", ancestor, descendant).Run() == nil
}

// isDirtyPathRefusal reports whether a `git merge --ff-only` failure is
// specifically git refusing because the fast-forward would overwrite a dirty
// path the incoming commits touch — the ONE case sync treats as a non-fatal,
// exit-0 report rather than an error. Every other failure (a stale
// index.lock, a missing/renamed ref, a corrupt repo, ...) keeps returning a
// real error so the caller can tell "refused" from "broken".
func isDirtyPathRefusal(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "would be overwritten by") || strings.Contains(s, "Not possible to fast-forward")
}

// reasonLine picks the one line of git's CombinedOutput worth reporting as
// "why" a merge failed: the first line starting with "error:" or "fatal:" —
// git's own diagnostic — falling back to the first non-empty line when
// neither is present. CombinedOutput interleaves stdout (git's "Updating
// a..b" progress) and stderr (the actual reason) only through stdio
// buffering order, so picking the first non-empty line unconditionally can
// surface the progress noise instead of the diagnostic.
func reasonLine(out []byte) string {
	var fallback string
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "fatal:") {
			return l
		}
		if fallback == "" {
			fallback = l
		}
	}
	return fallback
}
