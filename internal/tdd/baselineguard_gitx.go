package tdd

// TrunkBranch names the branch a lane is measured against: what the remote
// itself calls its default, else the configured `init.defaultBranch`, else
// the conventional names — and each candidate must actually resolve. A branch
// merely NAMED `master` beside a real trunk of another name is not trunk, so
// the conventional names come last and empty means "cannot tell". Exported
// for a consumer outside this package (the git shim's stale-branch push
// check, internal/cli) that needs the SAME trunk resolution every law already
// uses rather than re-deriving its own — one producer per derived datum.
func TrunkBranch(repoRoot string) string {
	if out, err := git(repoRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := lastNonEmptyLine(out); ref != "" {
			return ref
		}
	}
	if out, err := git(repoRoot, "config", "--get", "init.defaultBranch"); err == nil {
		if name := lastNonEmptyLine(out); name != "" {
			if _, err := git(repoRoot, "rev-parse", "--verify", "--quiet", name); err == nil {
				return name
			}
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := git(repoRoot, "rev-parse", "--verify", "--quiet", name); err == nil {
			return name
		}
	}
	return ""
}
