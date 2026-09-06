// Package gitenv holds the one piece of environment hygiene every package
// that shells out to git while it MAY be running nested inside a git hook
// needs: stripping the GIT_* variables the hook itself exports.
package gitenv

import (
	"os"
	"strings"
)

// Clean strips every GIT_* variable from the current environment. A git
// hook (pre-commit, post-commit, pre-merge-commit, ...) runs with GIT_DIR /
// GIT_INDEX_FILE / GIT_WORK_TREE / GIT_OBJECT_DIRECTORY and friends pointing
// at the repository the hook is running FOR. A git subprocess spawned from
// inside that hook that does not scrub these inherits them, and GIT_DIR
// takes precedence over any `-C <dir>` the subprocess was given — so it
// silently answers for whatever repository the hook's GIT_DIR names, not
// the directory the caller asked for. That is wrong whenever the two
// differ, which is the ordinary case for a caller reading a DIFFERENT
// worktree or a clone than the one the hook is committing.
//
// An allowlist (drop all GIT_*) is safer than blocklisting the handful of
// variables known to cause trouble.
//
// internal/tdd carries its own copy (cleanGitEnv in gitplumbing.go) layered
// with tdd-specific concerns (the git-queue-shim passthrough marker); this
// is the shared primitive both it and internal/ratchet build on, so the
// actual scrubbing logic exists in exactly one place.
func Clean() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}
