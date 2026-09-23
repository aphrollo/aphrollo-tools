package gitx

import (
	"strings"
)

// branchIsTrunk reports whether branch (a local branch name) names the same
// branch trunk resolved to, which may carry a "origin/" prefix of its own
// (trunkBranch's remote-HEAD route) that a local branch name never has.
func branchIsTrunk(branch, trunk string) bool {
	return branch == trunk || branch == strings.TrimPrefix(trunk, "origin/")
}
