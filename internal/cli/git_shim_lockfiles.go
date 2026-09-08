package cli

// The worktree scope's lock and owner files, distinct from the repo scope's
// (gitLockFileName / gitOwnerFileName). The primary checkout's own git dir
// IS the common dir, so a worktree-scoped `git merge` there — holding its
// lock for the whole pre-merge gate, hours when the merge measures mutants —
// used to sit on the very file every repo-scoped verb (`worktree add`,
// `branch -D`, `push`) waits for, and no lane could open or anchor itself
// until the merge landed.
const (
	gitWorktreeLockFileName  = "aphrollo-git-worktree.lock"
	gitWorktreeOwnerFileName = "aphrollo-git-worktree.owner"
)

// gitLockFileFor / gitOwnerFileFor pick the scope's own pair of files.
func gitLockFileFor(scope gitLockScope) string {
	if scope == gitWorktreeScope {
		return gitWorktreeLockFileName
	}
	return gitLockFileName
}

func gitOwnerFileFor(scope gitLockScope) string {
	if scope == gitWorktreeScope {
		return gitWorktreeOwnerFileName
	}
	return gitOwnerFileName
}
