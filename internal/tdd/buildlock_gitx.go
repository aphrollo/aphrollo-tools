package tdd

// GitQueuedEnv is the environment variable the git-queue shim (task A11)
// sets to "1" around a git invocation it already holds the per-repo git
// lock for. Exported so internal/cli's `tdd git` shim can check it (and
// BuildLockHeldEnv too) and pass straight through without touching the
// lock at all -- required because `git commit` (going through the shim,
// holding the lock) fires the pre-commit hook, which is aphrollo ITSELF,
// which spawns its own git subprocesses (worktree add/remove, apply, diff
// --cached, rev-parse, ...) that must never wait on the very lock their own
// parent process currently holds.
const GitQueuedEnv = "APHROLLO_GIT_QUEUED"
