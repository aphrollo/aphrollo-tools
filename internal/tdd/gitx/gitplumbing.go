package gitx

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
)

// --- git plumbing (scrubbed environment) ------------------------------------

// cleanGitEnv strips every GIT_* variable from the environment (gitenv.Clean
// — a git hook runs with GIT_DIR / GIT_INDEX_FILE / GIT_WORK_TREE /
// GIT_OBJECT_DIRECTORY and friends pointing at the OUTER repo; leaking any of
// them makes worktree commands operate on the wrong state) and layers this
// package's own belt-and-braces marker on top.
func cleanGitEnv() []string {
	out := gitenv.Clean()
	// Belt and braces (task A11): mark every git subprocess aphrollo itself
	// spawns as already-queued, so if one of these (worktree add/remove,
	// apply, diff --cached, rev-parse, ...) happens to route back through
	// the `tdd git` shim via PATH, it passes straight through instead of
	// waiting on the per-repo git lock its own parent process holds.
	out = append(out, GitQueuedEnv+"=1")
	return out
}

func git(dir string, args ...string) (string, error) {
	return gitStdin(dir, nil, args...)
}

// gitStdin runs git in dir with a scrubbed environment, optionally feeding stdin
// (nil for none), and returns the combined output. It is the single place the
// exec/clean-env/CombinedOutput pattern lives.
func gitStdin(dir string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	cmd.Stdin = stdin
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// MergeInProgressRefs is checked in order: the first of these refs that
// resolves is what Precommit reports and dispatches on. MERGE_HEAD covers a
// conflicted `git merge`; CHERRY_PICK_HEAD and REVERT_HEAD cover the
// identical situation for a conflicted `git cherry-pick`/`git revert` — all
// three fire git's pre-commit hook (not pre-merge-commit) when concluded
// with a manual `git commit`, and none of them should be judged by
// fail-first against the whole resulting diff.
// Exported because the git shim refuses a commit on the primary checkout
// unless it CONCLUDES one of these, and two lists would drift.
var MergeInProgressRefs = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"}

// mergeInProgressRef reports which of MergeInProgressRefs currently
// resolves in repoRoot (via `git rev-parse -q --verify <ref>`, which exits
// 0 only when the ref both exists and names a valid object), or "" if none
// does.
func mergeInProgressRef(repoRoot string) string {
	for _, ref := range MergeInProgressRefs {
		if _, err := git(repoRoot, "rev-parse", "-q", "--verify", ref); err == nil {
			return ref
		}
	}
	return ""
}

// stagedDiffFilter is what the gate considers a staged CHANGE: added, copied,
// modified, renamed or type-changed. R and T were missing, so a refactor
// commit — a rename plus an edit, which git records as R — produced zero gate
// activity for every renamed file it carried.
const stagedDiffFilter = "ACMRT"

// stagedFiles lists the changed paths in the index, as repo-root-relative
// paths, and reports an EMPTY set when git could not be asked at all. Only a
// caller for which "unknown" and "nothing" genuinely mean the same thing may
// use it; a gate deciding whether work was proven must use stagedFilesErr and
// refuse on the error.
func stagedFiles(repoRoot string) []string {
	files, _ := stagedFilesErr(repoRoot)
	return files
}

// stagedFilesErr is stagedFiles with the git failure kept. `-M` turns rename
// detection on explicitly (never inherited from the repo's diff.renames), and
// `--name-only` prints a rename's DESTINATION — the path that exists after the
// commit and the only one worth testing.
func stagedFilesErr(repoRoot string) ([]string, error) {
	args := []string{"diff", "--cached", "--name-only", "-M", "--diff-filter=" + stagedDiffFilter}
	if base := stagedDiffBase(repoRoot); base != "" {
		args = append(args, base)
	}
	out, err := git(repoRoot, args...)
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %v: %s", err, strings.TrimSpace(out))
	}
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// gitStaged returns the staged diff restricted to the given pathspecs.
func gitStaged(repoRoot string, paths []string) (string, error) {
	args := append([]string{"diff", "--cached", "--"}, paths...)
	return git(repoRoot, args...)
}

// gitApply applies a unified diff to a worktree via `git apply` on stdin.
func gitApply(wt, diff string) error {
	if out, err := gitStdin(wt, strings.NewReader(diff), "apply", "--whitespace=nowarn"); err != nil {
		return fmt.Errorf("git apply: %s", strings.TrimSpace(out))
	}
	return nil
}

// RepoRoot returns the git top-level for dir, or "" if dir is not in a repo —
// the working directory a git pre-commit hook should evaluate.
func RepoRoot(dir string) string {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(out))
}
