package tdd

import (
	"strings"
	"testing"
)

// The refusal names two ways to waive the merge-only rule:
//
//	override with `APHROLLO_PRIMARY_EDITS=1` or `/tdd primary-edits on`
//
// Neither is reachable from inside a turn. `/tdd primary-edits on` is a
// UserPromptSubmit hook — it fires on text a PERSON types, so an agent cannot
// invoke it. And the environment variable is read by the hook process, which
// is a different process from the shell that would export it: an
// `APHROLLO_PRIMARY_EDITS=1` set in one Bash call is gone by the next, and
// the PreToolUse hook that judges an Edit never sees it at all.
//
// So the documented override is documentation only, and the sessions that hit
// the rule legitimately have no way to act on what it tells them. The session
// id is already in the environment of anything the tool spawns
// (CLAUDE_SESSION_ID, which the build lock reads), so the same override needs
// a route the tool itself can take.
func TestSetPrimaryEditsFromEnvSession_WaivesTheRuleForThisSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const session = "s-primary-edits"
	t.Setenv("CLAUDE_SESSION_ID", session)

	if PrimaryEditsAllowed(session) {
		t.Fatal("the rule must start in force, or this test proves nothing")
	}

	msg, err := SetPrimaryEditsForEnvSession(true)
	if err != nil {
		t.Fatalf("SetPrimaryEditsForEnvSession: %v", err)
	}
	if !PrimaryEditsAllowed(session) {
		t.Error("the override did not reach the session the environment names")
	}
	if !strings.Contains(strings.ToLower(msg), "allowed") {
		t.Errorf("message = %q, want it to say the rule is waived", msg)
	}
}

// ...and it turns back off, so a session that waived the rule for one action
// can restore it rather than carrying the waiver for the rest of its life.
func TestSetPrimaryEditsFromEnvSession_RestoresTheRule(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const session = "s-primary-edits-off"
	t.Setenv("CLAUDE_SESSION_ID", session)
	if _, err := SetPrimaryEditsForEnvSession(true); err != nil {
		t.Fatal(err)
	}

	if _, err := SetPrimaryEditsForEnvSession(false); err != nil {
		t.Fatalf("SetPrimaryEditsForEnvSession(false): %v", err)
	}
	if PrimaryEditsAllowed(session) {
		t.Error("the rule was not restored")
	}
}

// With no session in the environment there is nothing to override, and saying
// so is the whole value: silently succeeding would leave the caller believing
// a waiver is in force while every edit is still refused.
func TestSetPrimaryEditsFromEnvSession_RefusesWhenTheEnvironmentNamesNoSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// Both names, or this is not the no-session case: SessionID falls back to
	// CLAUDE_CODE_SESSION_ID, which the real environment running this test
	// does set.
	t.Setenv(sessionEnv, "")
	t.Setenv(sessionCodeEnv, "")

	if _, err := SetPrimaryEditsForEnvSession(true); err == nil {
		t.Error("SetPrimaryEditsForEnvSession returned no error with no session to apply it to")
	}
}
