package undercover

import (
	"os"
	"strings"
)

// RemoteEnv is set to "true" by Claude Code in a cloud (remote) session.
// AssignedBranchEnv carries the one branch that session was assigned. Claude
// Code publishes no variable for the assigned branch, so the session's setup
// names it explicitly (APHROLLO_ASSIGNED_BRANCH=<branch>); the name is read
// only inside a remote session, so a local shell that sets it changes nothing.
const (
	RemoteEnv         = "CLAUDE_CODE_REMOTE"
	AssignedBranchEnv = "APHROLLO_ASSIGNED_BRANCH"
)

// The exemption trusts two environment variables the session itself controls,
// so it is a policy fence against accidental tells, not a security boundary.
//
// isAssignedBranch reports whether name is, byte for byte, the branch a
// remote session was assigned. name is a bare name or a full refs/heads/ path;
// a refs/tags/ path (or any other ref) is never the assigned branch. Never a
// pattern: a prefix, another case or a sibling branch is still a name the
// session chose for itself.
func isAssignedBranch(name string) bool {
	if os.Getenv(RemoteEnv) != "true" {
		return false
	}
	name = strings.TrimPrefix(name, "refs/heads/")
	assigned := os.Getenv(AssignedBranchEnv)
	return assigned != "" && name == assigned
}
