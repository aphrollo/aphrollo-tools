package tdd

import (
	"io"
	"strings"
)

// The post-merge git hook. `aphrollo workspace merge` has always ended with
// the guarded lane sweep (PruneMergedLanesAfterMerge); a plain `git merge`
// got nothing, and that gap is what a hand-written `post-merge` hook filled
// on one box — with a `git merge-base --is-ancestor` test that reads a
// FRESH lane sitting at trunk's tip as merged, and deleted one out from
// under a working builder (issue #582).
//
// Closing the gap means running the guarded sweep from git's own hook. But
// core.hooksPath is MACHINE-WIDE: this hook fires in every repo on the box,
// and git runs it after a `git pull` exactly as after a `git merge`. A sweep
// that removes worktrees and deletes branches must therefore be OPT-IN per
// repo — a repo that never asked is left alone entirely, output included.
// There is deliberately no "sweep everything by default" path here, and the
// sweep's own guards (#144, #382) are untouched: this file only decides
// WHETHER to call it, and from where.

// pruneLanesOnMergeKey is the repo-declared opt-in, read from
// `[workspace.metadata.aphrollo]` in a Cargo workspace's manifest or
// `[aphrollo]` in aphrollo.toml — the same two tables, in the same order,
// that `mutants-at-merge` is read from.
const pruneLanesOnMergeKey = "prune-lanes-on-merge"

// PruneLanesOnMerge reports whether the repo at root asked for the
// post-merge lane sweep. Absent is false and false is false: only a repo
// that WROTE the key true gets a destructive sweep from a hook it never
// installed itself.
func PruneLanesOnMerge(root string) bool {
	for _, t := range mutantsConfigTables(root) {
		if v, set := tomlBoolSetIn(t.path, t.table, pruneLanesOnMergeKey); set {
			return v
		}
	}
	return false
}

// PostMergeSweep is the `post-merge` hook's whole body: in a repo that
// declared prune-lanes-on-merge, sweep the merged lanes of the repo the
// merge landed in. dir is where git ran the hook — the worktree holding the
// merge — and it is passed as the sweep's exclude, so the ground the hook
// process is standing on is never removed however merged its own branch may
// now be (a lane that just pulled trunk into itself is exactly that case).
//
// The sweep itself runs against the PRIMARY checkout, since that is where
// `git worktree list` and the branch refs live for every worktree of the
// repo, and it is the sweep's existing contract.
//
// Every early return is silent. A hook that fires on every merge and every
// pull in every repo on the box has to be inert when it has nothing to say:
// a "prune-lanes:" line printed in a repo that never opted in is how an
// operator comes to read this tool as the thing deleting their work.
func PostMergeSweep(dir string, stdout, stderr io.Writer) []PrunedLane {
	root := RepoRoot(dir)
	if root == "" {
		return nil // not a repository, or git is missing — say nothing
	}
	if !PruneLanesOnMerge(root) {
		return nil // the repo never asked
	}
	primary := primaryCheckoutRoot(root)
	if primary == "" {
		return nil
	}
	return PruneMergedLanesAfterMerge(primary, root, stdout, stderr)
}

// PostCommitMergeSweep is the `post-commit` hook's own call into the guarded
// lane sweep — issue #716's second cause. A lane landed by resolving a
// conflict and concluding with a plain `git commit` never fires git's own
// `post-merge` hook at all: proven empirically against real git, post-merge
// fires only from `git merge` completing on its own, never from the `git
// commit` that finishes a conflict by hand. PostMergeSweep alone therefore
// swept a clean `git merge --no-ff` landing but silently left a
// conflict-resolved one behind — the exact "sometimes it cleans up, sometimes
// it doesn't" the issue reports, with the same command shape (`git merge
// --no-ff ... -F msg`, concluded with `git commit -F` after a conflict) at
// its root.
//
// post-commit fires for EVERY commit in every repo on the box — the same
// machine-wide cost PostMergeSweep already carries for post-merge — so this
// is opt-in on the same key and cheap-checked before touching a worktree
// list: headConcludedByCommit reads one reflog line, and only a commit shaped
// exactly like a conclude-after-conflict merge reaches the sweep at all. A
// clean automatic merge's commit is shaped differently and is left to
// PostMergeSweep, which already covers it — reaching for the sweep from both
// hooks on the same merge would be harmless (idempotent) but would print a
// second, redundant line, and this repo's rule is one line per sweep that
// found nothing, not two.
func PostCommitMergeSweep(dir string, stdout, stderr io.Writer) []PrunedLane {
	root := RepoRoot(dir)
	if root == "" {
		return nil // not a repository, or git is missing — say nothing
	}
	if !PruneLanesOnMerge(root) {
		return nil // the repo never asked
	}
	if !headConcludedByCommit(root) {
		return nil // not a conflict resolved by hand — post-merge already covers whatever this is
	}
	primary := primaryCheckoutRoot(root)
	if primary == "" {
		return nil
	}
	return PruneMergedLanesAfterMerge(primary, root, stdout, stderr)
}

// headConcludedByCommit reports whether the commit just made at HEAD is a
// merge concluded by plain `git commit` after a conflict, rather than one
// `git merge` completed on its own. Git's own reflog writes the two shapes
// differently — "commit (merge): <msg>" for the former, "merge <ref>: <msg>"
// for the latter — which is the one fact still readable after the fact: by
// the time any hook fires, MERGE_HEAD is already gone and the commit itself
// carries no marker of how it was concluded. Anything else (an ordinary
// commit, an amend, an unreadable reflog) answers false.
func headConcludedByCommit(repoRoot string) bool {
	out, err := git(repoRoot, "reflog", "show", "-1", "--format=%gs", "HEAD")
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(out), "commit (merge): ")
}
