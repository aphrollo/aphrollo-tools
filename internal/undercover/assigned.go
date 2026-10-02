package undercover

import "os"

// RemoteEnv is set to "true" by Claude Code in a cloud (remote) session.
// AssignedBranchEnv carries the one branch that session was assigned. Claude
// Code publishes no variable for the assigned branch, so the session's setup
// names it explicitly (APHROLLO_ASSIGNED_BRANCH=<branch>); the name is read
// only inside a remote session, so a local shell that sets it changes nothing.
const (
	RemoteEnv         = "CLAUDE_CODE_REMOTE"
	AssignedBranchEnv = "APHROLLO_ASSIGNED_BRANCH"
)

// isAssignedBranch reports whether name is, byte for byte, the branch a
// remote session was assigned. Never a pattern: a prefix, another case or a
// sibling branch is still a name the session chose for itself.
func isAssignedBranch(name string) bool {
	if os.Getenv(RemoteEnv) != "true" {
		return false
	}
	assigned := os.Getenv(AssignedBranchEnv)
	return assigned != "" && name == assigned
}
