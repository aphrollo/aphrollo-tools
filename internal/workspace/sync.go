package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
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
//   - SOME worktree of the repo has the default branch checked out => merge
//     --ff-only IN THAT WORKTREE, so its HEAD, index and files move together.
//     A dirty worktree is NOT refused up front: git itself refuses a
//     fast-forward only when it would overwrite an uncommitted change to a
//     path the incoming commits touch, so sync just asks git and reports git's
//     own reason — an unrelated dirty file never blocks a fast-forward it
//     doesn't conflict with.
//   - NO worktree has it checked out => advance the ref alone, and say so.
//   - NEVER reset --hard, NEVER force, NEVER move the branch when it has
//     diverged (local commits ahead) — leave it, say why.
//
// Only the default branch is synced; ticket branches and other worktrees are
// left alone. Re-running on an already-current clone is a no-op success. --dry
// previews the fast-forward (the fetch still runs — it only touches
// remote-tracking refs) and mutates nothing else.
func Sync(repoArg string, dry bool, stdout, stderr io.Writer) error {
	top, err := resolveSyncRepo(repoArg)
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

	// WHICH checkout holds the default branch — not "is it the one we were
	// pointed at" — decides the shape of the move. refs/heads/<def> is shared by
	// every worktree of the repo, so moving that ref on its own while a checkout
	// sits on it leaves that checkout past its own HEAD: index and files still at
	// the old commit, `git status` there reporting the commit that just landed as
	// a STAGED REVERT, and the next commit made in it silently undoing it
	// (issue #618). A held branch is therefore only ever advanced BY its holder.
	holder := worktreeOnBranch(top, def)

	if dry {
		fmt.Fprintf(stdout, "would fast-forward %s to %s (%d commit(s) behind)\n", def, remote, behind)
		return nil
	}

	if holder != "" {
		// --ff-only run in the holding checkout advances its HEAD, index and
		// files together. It also refuses anything that isn't a fast-forward —
		// including a dirty tracked path the incoming commits would overwrite —
		// so git is the judge of "safe to move", not a pre-check here. A refusal
		// is reported, never treated as an error: it is non-destructive, and the
		// reason is git's own.
		if out, err := exec.Command("git", "-C", holder, "merge", "--ff-only", remote).CombinedOutput(); err != nil {
			if !isDirtyPathRefusal(out) {
				fmt.Fprint(stderr, string(out))
				return fmt.Errorf("git merge --ff-only %s: %w", remote, err)
			}
			fmt.Fprintf(stdout, "%s could not fast-forward: %s\n", def, reasonLine(out))
			return nil
		}
		fmt.Fprintf(stdout, "fast-forwarded %s to %s (%d commit(s))%s\n", def, remote, behind, inOtherCheckout(top, holder))
		return nil
	}

	// No worktree has the default branch checked out: nothing can be stranded, so
	// advance the ref alone. `git branch --force` rather than `update-ref` is the
	// point — it is the ref move git ITSELF refuses when a worktree holds the
	// branch, so a wrong or raced answer above costs a refusal, never a checkout
	// left behind its own HEAD. The message says "ref only" in full words: the
	// working tree of the repo is NOT at the new tip and a reader must not have
	// to run git to know that.
	// --no-track keeps the move to the ref ALONE, exactly like the update-ref it
	// replaces: without it, `branch --force` re-runs branch.autoSetupMerge and
	// rewrites branch.<def>.remote/merge as a side effect of a fast-forward.
	if out, err := exec.Command("git", "-C", top, "branch", "--force", "--no-track", def, remote).CombinedOutput(); err != nil {
		if isCheckedOutRefusal(out) {
			fmt.Fprintf(stdout, "%s ref NOT moved — a checkout holds it: %s\n", def, reasonLine(out))
			fmt.Fprintf(stdout, "  fast-forward it from that checkout: git merge --ff-only %s\n", remote)
			return nil
		}
		fmt.Fprint(stderr, string(out))
		return fmt.Errorf("git branch --force %s %s: %w", def, remote, err)
	}
	fmt.Fprintf(stdout, "fast-forwarded %s ref to %s (%d commit(s)) — ref only, no checkout is on %s\n", def, remote, behind, def)
	return nil
}

// inOtherCheckout names the working tree a fast-forward actually landed in when
// it is NOT the repo sync was pointed at — a sibling worktree holding the
// default branch. Empty for the ordinary case (the clone itself is on it), so
// the common line stays exactly as it was.
func inOtherCheckout(top, holder string) string {
	if samePath(top, holder) {
		return ""
	}
	return " in " + filepath.ToSlash(holder)
}

// worktreeOnBranch returns the path of the worktree that has branch def checked
// out, or "" when no checkout is on it. `git worktree list --porcelain` is the
// authoritative answer for the WHOLE repo — the main working tree and every
// linked one — which is what a shared-ref move has to respect: refs/heads/<def>
// belongs to all of them, so asking only "what is checked out HERE" cannot tell
// whether moving it strands somebody. A read failure answers "" (unknown), and
// the `git branch --force` that follows is what actually refuses the unsafe
// move.
func worktreeOnBranch(repo, def string) string {
	// This is a probe, not the decision: an unreadable list answers "unknown" and
	// the `git branch --force` below refuses the unsafe move on its own, carrying
	// git's OWN stderr — surfacing this one too would report a failure the caller
	// is never asked to act on.
	// stderr-ok: a probe whose failure is already covered by the refusal below.
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	want := "refs/heads/" + def
	var path string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			path = filepath.Clean(strings.TrimSpace(rest))
			continue
		}
		if rest, ok := strings.CutPrefix(line, "branch "); ok && strings.TrimSpace(rest) == want {
			return path
		}
	}
	return ""
}

// isCheckedOutRefusal reports whether a `git branch --force` failure is git
// refusing to move a branch some worktree has checked out — "cannot force
// update the branch 'main' used by worktree at '<path>'", and the older
// "Cannot force update the current branch." for the one you are standing in.
// That refusal is a safe outcome (the ref did not move), so sync reports it and
// exits 0; every other failure stays a real error.
func isCheckedOutRefusal(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "used by worktree at") ||
		strings.Contains(s, "checked out at") ||
		strings.Contains(s, "force update the current branch")
}

// resolveSyncRepo turns Sync's optional repoArg into a repo toplevel: empty
// resolves the caller's cwd repo, the same rule commit's cwd-only resolution
// uses (ResolveTarget with both args empty), so `sync` with no argument
// matches what a coder standing in a worktree expects.
func resolveSyncRepo(repoArg string) (string, error) {
	if repoArg == "" {
		t, err := ResolveTarget("", "", "")
		if err != nil {
			return "", err
		}
		return t.MainRepo, nil
	}
	return resolveMainRepo(repoArg)
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

// reasonMaxLines caps how many lines reasonLine returns for a single
// refusal: the error:/fatal: sentence plus its indented continuation. Git's
// "would be overwritten" refusal lists one indented path per conflicting
// file, and a pathological many-file conflict must not turn into an
// unbounded stdout line — a handful of paths is already enough to act on.
const reasonMaxLines = 8

// reasonLine picks the lines of git's CombinedOutput worth reporting as "why"
// a merge failed: starting from the first line prefixed "error:" or
// "fatal:" — git's own diagnostic — it also keeps every line immediately
// after that is indented (tab or space), because that is where git puts the
// offending path(s) ("would be overwritten by merge:\n\tbase.txt"); dropping
// those leaves the generic sentence with the actionable part stripped off.
// It stops at the first line that is neither indented nor another
// error:/fatal: line — that drops git's trailing "Please commit your
// changes..." / "Aborting" boilerplate, which is noise. Falls back to the
// first non-empty line when no error:/fatal: line is present at all.
// CombinedOutput interleaves stdout (git's "Updating a..b" progress) and
// stderr (the actual reason) only through stdio buffering order, so picking
// the first non-empty line unconditionally can surface the progress noise
// instead of the diagnostic.
func reasonLine(out []byte) string {
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "fatal:") {
			kept := []string{l}
			for _, cont := range lines[i+1:] {
				if len(kept) >= reasonMaxLines {
					break
				}
				if strings.TrimSpace(cont) == "" {
					continue
				}
				indented := strings.HasPrefix(cont, " ") || strings.HasPrefix(cont, "\t")
				contTrimmed := strings.TrimSpace(cont)
				isDiagLine := strings.HasPrefix(contTrimmed, "error:") || strings.HasPrefix(contTrimmed, "fatal:")
				if !indented && !isDiagLine {
					break
				}
				kept = append(kept, contTrimmed)
			}
			return strings.Join(kept, "\n")
		}
	}
	for _, line := range lines {
		if l := strings.TrimSpace(line); l != "" {
			return l
		}
	}
	return ""
}
