package tdd

import "os"

// Claude Code sets CLAUDE_CODE_SESSION_ID in the environment it hands to
// tools. Every session-scoped feature here was written against
// CLAUDE_SESSION_ID, which nothing sets — so `gate allow` reported "no session
// in the environment" to every caller, human or not, and the build lock
// recorded every holder with an empty session id, leaving contention
// unattributable across parallel sessions.
//
// Both names are read, in that order, so an environment that does set the
// short name keeps working.
const (
	sessionEnv     = "CLAUDE_SESSION_ID"
	sessionCodeEnv = "CLAUDE_CODE_SESSION_ID"
)

// SessionID answers the id of the session this process belongs to, or "" when
// it belongs to none.
func SessionID() string {
	if s := os.Getenv(sessionEnv); s != "" {
		return s
	}
	return os.Getenv(sessionCodeEnv)
}
