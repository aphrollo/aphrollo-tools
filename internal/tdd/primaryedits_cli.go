package tdd

import (
	"errors"
	"os"
)

// SetPrimaryEditsForEnvSession waives (or restores) the primary-checkout
// merge-only rule for the session the ENVIRONMENT names, and returns the line
// to print.
//
// The refusal names two overrides and neither was reachable from inside a
// turn. `/tdd primary-edits on` is a UserPromptSubmit hook, so it fires on
// text a person types and an agent cannot invoke it. `APHROLLO_PRIMARY_EDITS=1`
// is read by the hook process, which is a different process from the shell
// that would export it — the variable is gone by the next call, and the
// PreToolUse hook judging an Edit never sees it. So a session that hit the
// rule legitimately had no way to act on what the message told it to do.
//
// CLAUDE_SESSION_ID is already in the environment of everything the tool
// spawns (the build lock reads it), which makes it the one identifier a
// command run from inside a turn can rely on.
func SetPrimaryEditsForEnvSession(on bool) (string, error) {
	session := os.Getenv("CLAUDE_SESSION_ID")
	if session == "" {
		return "", errors.New("no session in the environment (CLAUDE_SESSION_ID is unset), so there is nothing to override — an edit would still be refused")
	}
	if err := setPrimaryEdits(session, on); err != nil {
		return "", err
	}
	logOverride("override-primary-edits-"+onOff(on), session, "")
	if on {
		return "Primary-checkout edits ALLOWED for this session — the merge-only rule is waived. Run `aphrollo gate primary-edits off` to restore it.", nil
	}
	return "Primary-checkout edits refused again for this session.", nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
