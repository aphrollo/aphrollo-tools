package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Update rebases the cwd worktree's branch onto the fresh tip of the repo's
// default remote branch and, on a clean rebase, force-pushes (with lease) to
// refresh the open PR. It is the "catch my branch up to origin/<default>" verb:
//
//   - git fetch origin — refresh the remote-tracking refs.
//   - rebase HEAD onto origin/<default> (resolved, never hardcoded "main").
//   - CLEAN rebase + the branch has a remote => git push --force-with-lease, so
//     the PR shows the rebased branch; report the new ahead/behind + the push.
//   - CONFLICT => leave the rebase IN PROGRESS (no blind abort, no push), print
//     the conflicted files and how to resolve, and return a non-nil error.
//
// Idempotent: a branch already on top of origin/<default> is a no-op. --dry
// reports the behind-count and that it WOULD rebase, mutating nothing.
func Update(t *Target, dry bool, stdout, stderr io.Writer) error {
	if t.Branch == "HEAD" {
		return fmt.Errorf("detached HEAD in %s — check out a branch before updating", t.Worktree)
	}
	wt := t.Worktree

	// Refuse up-front on a dirty worktree: git's rebase aborts on uncommitted
	// changes anyway, but with an opaque message and only after the fetch. Surface
	// it cleanly here so the caller is never left in a half-updated state.
	if !worktreeClean(wt) {
		return fmt.Errorf("worktree has uncommitted changes — commit or stash before updating (git stash → update → git stash pop)")
	}

	def := resolveDefaultBranch(wt)

	// Refresh origin so the behind-count and rebase base are the live tip. Fetch
	// touches only remote-tracking refs, never HEAD or the working tree, so it is
	// safe in --dry too.
	if out, err := gitNetworkOutput(wt, "fetch", "origin", "--quiet"); err != nil {
		// A remote-less / offline repo can't update; surface it but don't crash.
		fmt.Fprintf(stderr, "git fetch origin: %v\n%s\n", err, strings.TrimSpace(string(out)))
	}

	base := def
	if remote := "origin/" + def; gitRefExists(wt, remote) {
		base = remote
	}

	behind, ok := gitBehindCount(wt, "HEAD", base)
	if !ok {
		return fmt.Errorf("could not resolve %s — fetch origin or check the default branch", base)
	}
	if behind == 0 {
		fmt.Fprintf(stdout, "already current with %s (no rebase needed)\n", base)
		return nil
	}

	if dry {
		fmt.Fprintf(stdout, "behind %s by %d commit(s); would rebase onto it\n", base, behind)
		return nil
	}

	// Rebase HEAD onto the fresh base.
	out, err := exec.Command("git", "-C", wt, "rebase", base).CombinedOutput()
	if err != nil {
		// A conflict leaves the rebase in progress. Do NOT abort and do NOT push —
		// the user resolves, then continues (or aborts to back out).
		conflicts := conflictedFiles(wt)
		fmt.Fprint(stderr, string(out))
		fmt.Fprintf(stdout, "rebase onto %s hit conflicts in:\n", base)
		for _, f := range conflicts {
			fmt.Fprintf(stdout, "  %s\n", f)
		}
		fmt.Fprintf(stdout, "resolve, then: git rebase --continue  (or: git rebase --abort to back out)\n")
		return fmt.Errorf("rebase onto %s conflicted — left in progress for you to resolve", base)
	}
	fmt.Fprintf(stdout, "rebased %s onto %s\n", t.Branch, base)

	// Update the PR only when the branch is already on the remote (an open PR's
	// head). A branch never pushed has nothing to update — say so, don't push.
	if !remoteBranchExists(wt, t.Branch) && upstreamRef(wt) == "" {
		fmt.Fprintf(stdout, "not on origin yet — run: aphrollo workspace push\n")
		return nil
	}
	pushOut, perr := gitNetworkOutput(wt, "push", "--force-with-lease", "origin", "--", t.Branch)
	if perr != nil {
		fmt.Fprint(stderr, string(pushOut))
		return fmt.Errorf("git push --force-with-lease: %w", perr)
	}
	ahead := aheadCount(wt, base)
	fmt.Fprintf(stdout, "pushed %s -> origin --force-with-lease (%s commit(s) ahead of %s)\n", t.Branch, ahead, base)
	if url := branchURL(wt, t.Branch); url != "" {
		fmt.Fprintf(stdout, "  %s\n", url)
	}
	return nil
}

// conflictedFiles returns the paths with unresolved merge conflicts in the
// worktree (`git diff --name-only --diff-filter=U`).
func conflictedFiles(wt string) []string {
	out, err := exec.Command("git", "-C", wt, "diff", "--name-only", "--diff-filter=U").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.TrimSpace(line); f != "" {
			files = append(files, f)
		}
	}
	return files
}
