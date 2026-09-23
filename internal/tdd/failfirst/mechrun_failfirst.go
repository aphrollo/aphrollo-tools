package failfirst

// resolvedDevTarget is where a build in repoRoot lands by default: the
// environment's CARGO_TARGET_DIR if set, else the workspace root's target/.
// The fail-first run must EXPORT this rather than inherit it — its worktree
// lives elsewhere, so cargo's default would silently create a second one.
func resolvedDevTarget(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	return ResolveCargoTargetDir(repoRoot)
}
