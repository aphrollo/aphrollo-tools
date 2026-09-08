package cli

// gitLockScope is WHICH git directory a verb's lock belongs in. The index
// is per worktree (`index.lock` lives in that worktree's own git dir), so
// keying every mutation on the shared common dir made one lane's commit gate
// -- which holds its lock for the whole gate run -- block `git add` in every
// other worktree of the same repo.
type gitLockScope int

const (
	gitNoLock gitLockScope = iota
	// gitWorktreeScope: the invocation mutates THIS worktree's index or
	// HEAD. Lock lives in `git rev-parse --git-dir`.
	gitWorktreeScope
	// gitRepoScope: the invocation mutates state every worktree of the repo
	// shares -- branches, the worktree registry, the object store. Lock
	// lives in `git rev-parse --git-common-dir`.
	gitRepoScope
)

// gitIndexVerbs mutate the invoking worktree's index/HEAD regardless of
// their own flags. `pull` and `merge` are here rather than in the shared set
// because what they contend for is the index they write into; git does its
// own ref locking underneath.
var gitIndexVerbs = map[string]bool{
	"add":         true,
	"commit":      true,
	"checkout":    true,
	"switch":      true,
	"reset":       true,
	"rm":          true,
	"mv":          true,
	"rebase":      true,
	"cherry-pick": true,
	"revert":      true,
	"am":          true,
	"merge":       true,
	"pull":        true,
}

// gitSharedVerbs mutate state shared by every worktree of the repo.
var gitSharedVerbs = map[string]bool{
	"fetch": true,
	"push":  true,
	"gc":    true,
}

// gitStashReadingSubverbs are the `git stash` forms that write nothing --
// they read refs/stash and print. Issue #575: classifying `stash` by its
// verb alone put `git stash list` in the queue, where it waited out a merge
// gate compiling a Rust workspace. Every other form (a bare `git stash`, an
// unrecognized one, `push`/`pop`/`apply`/`drop`/`clear`/`branch`) keeps the
// lock -- the safe direction, since a read made to wait costs a wait, while
// a write let past the queue costs the index.
var gitStashReadingSubverbs = map[string]bool{
	"list": true,
	"show": true,
}

// gitBranchMutationFlags are the `git branch` forms that write refs; a bare
// `git branch` (or --list) only reads, and must never take a lock.
var gitBranchMutationFlags = map[string]bool{
	"-d": true, "-D": true, "--delete": true,
	"-m": true, "-M": true, "--move": true,
	"-c": true, "-C": true, "--copy": true,
}

// gitLockScopeFor classifies rest (args with any leading global options
// already stripped, per gitGlobalArgs): no lock, the per-worktree index
// lock, or the repo-wide one. restore/apply/branch/worktree are conditional
// on their own flags or sub-verb; everything else is a fixed lookup.
func gitLockScopeFor(rest []string) gitLockScope {
	if len(rest) == 0 {
		return gitNoLock
	}
	switch verb := rest[0]; verb {
	case "restore":
		if containsToken(rest[1:], "--staged") {
			return gitWorktreeScope
		}
		return gitNoLock
	case "apply":
		if containsToken(rest[1:], "--index") || containsToken(rest[1:], "--cached") {
			return gitWorktreeScope
		}
		return gitNoLock
	case "worktree":
		if len(rest) < 2 {
			return gitNoLock
		}
		switch rest[1] {
		case "add", "remove", "prune":
			return gitRepoScope
		}
		return gitNoLock
	case "stash":
		if len(rest) > 1 && gitStashReadingSubverbs[rest[1]] {
			return gitNoLock
		}
		return gitWorktreeScope
	case "branch":
		for _, a := range rest[1:] {
			if gitBranchMutationFlags[a] {
				return gitRepoScope
			}
		}
		return gitNoLock
	default:
		if gitIndexVerbs[verb] {
			return gitWorktreeScope
		}
		if gitSharedVerbs[verb] {
			return gitRepoScope
		}
		return gitNoLock
	}
}

// containsToken reports whether needle appears as an EXACT element of args
// -- not a prefix/substring match, since e.g. "--staged" and
// "--staged-with-tree" are different flags.
func containsToken(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}
