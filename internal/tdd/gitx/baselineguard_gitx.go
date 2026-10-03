package gitx

// TrunkBranch names the branch a lane is measured against: what the remote
// itself calls its default, else the configured `init.defaultBranch`, else
// the conventional names — and each candidate must actually resolve. A branch
// merely NAMED `master` beside a real trunk of another name is not trunk, so
// the conventional names come last and empty means "cannot tell". Exported
// for a consumer outside this package (the git shim's stale-branch push
// check, internal/cli) that needs the SAME trunk resolution every law already
// uses rather than re-deriving its own — one producer per derived datum.
func TrunkBranch(repoRoot string) string {
	c := HookClient(repoRoot)
	if c == nil {
		return ""
	}
	return c.Trunk()
}
