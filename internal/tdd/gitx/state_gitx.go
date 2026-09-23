package gitx

import (
	"os/exec"
	"strings"
)

// gitOut reads a git value from root. It runs outside the hook context (the
// PostToolUse fingerprint, not a pre-commit worktree), so it intentionally skips
// cleanGitEnv() — no inherited GIT_* vars to scrub here.
func gitOut(root string, args ...string) string {
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
