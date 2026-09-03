package tdd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// A mutation run rewrites the tree it runs in, and a gate's own tests are git
// tests: under mutation they run init, commit, merge, worktree add and config
// core.bare, and a test that cannot resolve the checkout it meant falls back
// to the one it is standing in. Run inside a LINKED worktree that is the
// repository every other worktree of the repo resolves its refs through —
// local main moved to a fake "init" commit, a lane branch was rewritten and
// core.bare flipped to true on the real repository (issue #156).
//
// So the Go job never runs in a linked worktree. It runs in a clone made with
// `--shared`, which copies no objects (the alternates point at the source's
// own store) but gives the run its own refs, HEAD, index and config — the
// four things a mutated test can wreck.

// goMutantsTree resolves the directory the Go mutation run must happen in:
// the job's own worktree when that is already a repository of its own, a
// private clone of the lane tip when it is not.
func goMutantsTree(j MutantsJob) (string, error) {
	if j.Worktree == "" || j.Tip == "" {
		return "", errors.New("job names no worktree")
	}
	if !isLinkedWorktree(j.Worktree) {
		return j.Worktree, nil
	}
	return cloneMutantsRunTree(j)
}

// isLinkedWorktree asks git rather than the path's spelling: a linked
// worktree's --git-dir is <common>/worktrees/<name>, a standalone clone's is
// its own --git-common-dir. A directory git cannot answer for is left alone —
// the run will fail on its own terms rather than on a guess made here.
func isLinkedWorktree(dir string) bool {
	gitDir := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	common := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gitDir == "" || common == "" {
		return false
	}
	return !sameGitDir(dir, gitDir, common)
}

// goMutantsCloneDir is where that private clone lives: BESIDE the mutation
// worktree, never inside it — a clone under the tree gremlins walks would be
// mutated along with it, and would make the worktree's own dirty check read
// the clone's files.
func goMutantsCloneDir(worktree string) string {
	return filepath.Clean(worktree) + "-clone"
}

// cloneMutantsRunTree builds that clone from scratch every run. Reusing one
// would mean trusting refs, HEAD and config a previous mutation run was free
// to rewrite — the exact state this whole mechanism exists to contain — and
// `--shared` makes the rebuild an object-free checkout rather than a copy of
// the repository.
func cloneMutantsRunTree(j MutantsJob) (string, error) {
	dir := goMutantsCloneDir(j.Worktree)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	source := primaryCheckoutRoot(j.Worktree)
	if source == "" {
		source = j.RepoRoot
	}
	if out, err := git(filepath.Dir(dir), "clone", "--shared", "--quiet", "--no-checkout", source, dir); err != nil {
		return "", errors.New(strings.TrimSpace(out))
	}
	// The tip by sha, not by branch: the lane may have moved on since the
	// commit that started this job, and the run describes THAT commit. The
	// object is readable through the clone's alternates without a fetch.
	if out, err := git(dir, "checkout", "--quiet", "--detach", "--force", j.Tip); err != nil {
		return "", errors.New(strings.TrimSpace(out))
	}
	return dir, nil
}
