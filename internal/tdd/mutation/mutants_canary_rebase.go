package mutation

import (
	"os"
	"path/filepath"
	"strings"
)

// withRebasingBranches is a worktree-HEADs text with each worktree that is
// stopped in the middle of a rebase shown on the branch it is rebasing: git
// detaches HEAD for the length of a rebase and puts the branch back at its
// end, so another lane rebasing while a run goes is that lane's own work and
// no change of git state the run could have made (#1144). A detached HEAD with
// no rebase behind it is left as it reads.
func withRebasingBranches(heads string) string {
	var out []string
	for line := range strings.SplitSeq(heads, "\n") {
		if path, state, ok := strings.Cut(line, " -> "); ok && state == "detached" {
			if ref := rebasingBranch(path); ref != "" {
				line = path + " -> " + ref
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// rebasingBranch is the branch ref the rebase in progress at the worktree path
// started from, "" when none is going.
func rebasingBranch(path string) string {
	dir := strings.TrimSpace(gitOut(path, "rev-parse", "--absolute-git-dir"))
	if dir == "" {
		return ""
	}
	for _, state := range []string{"rebase-merge", "rebase-apply"} {
		data, err := os.ReadFile(filepath.Join(dir, state, "head-name"))
		if ref := strings.TrimSpace(string(data)); err == nil && strings.HasPrefix(ref, "refs/heads/") {
			return ref
		}
	}
	return ""
}
