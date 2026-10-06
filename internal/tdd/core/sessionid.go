package core

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

// The session and agent a hook process is serving, read from its payload. A
// hook is one short process answering one call, so this is set once, where the
// hook starts, and AppendEvent reads it: every event the hook appends is then
// attributed to "session/agent" (a subagent's call) or to the session, with
// no call site saying so.
var hookSession, hookAgent string

// SetHookActor names the session, and for a subagent's call the agent, that the
// hook process serves. Both empty clears it.
func SetHookActor(session, agent string) {
	hookSession, hookAgent = session, agent
}

// eventActor is the actor an event is recorded under: the actor it names, or
// the hook's session (else the environment's) when it names none, and
// "session/agent" when the hook serves a subagent's call and the event is its
// session's. An event of another session, or of no session, is left as it is.
func eventActor(actor string) string {
	if actor == "" {
		actor = hookSession
	}
	if actor == "" {
		actor = SessionID()
	}
	if hookAgent != "" && actor == hookSession {
		return hookSession + "/" + hookAgent
	}
	return actor
}
