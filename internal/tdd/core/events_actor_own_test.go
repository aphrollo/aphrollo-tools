package core

import (
	"encoding/json"
	"testing"
)

func actorOfLastEvent(t *testing.T, root string) string {
	t.Helper()
	lines := eventsLines(t, root)
	var e struct {
		Actor string `json:"actor"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &e); err != nil {
		t.Fatal(err)
	}
	return e.Actor
}

// A subagent's hook calls carry its agent id, and the report could not tell
// which builder worked in which lane while only hook.timing events said so. The
// actor is set once, where an event is appended: a hook that knows its session
// and agent stamps every event of the process "session/agent".
func TestAppendEvent_AHookWithAnAgentRecordsSessionSlashAgent(t *testing.T) {
	isolateEvents(t)
	main, _ := laneRoot(t, "lane/x")
	SetHookActor("sid-1", "aid-9")
	t.Cleanup(func() { SetHookActor("", "") })

	AppendEvent(Event{Kind: "edit", Root: main, Actor: "sid-1"})
	if got := actorOfLastEvent(t, main); got != "sid-1/aid-9" {
		t.Errorf("an edit event with the session's plain id carries %q, want sid-1/aid-9", got)
	}
	AppendEvent(Event{Kind: "commit_gate", Root: main, Stage: "precommit"})
	if got := actorOfLastEvent(t, main); got != "sid-1/aid-9" {
		t.Errorf("an event with no actor carries %q, want sid-1/aid-9", got)
	}
	AppendEvent(Event{Kind: "edit", Root: main, Actor: "another-session"})
	if got := actorOfLastEvent(t, main); got != "another-session" {
		t.Errorf("another session's event carries %q, want it untouched", got)
	}
}

// With no agent id the actor is the session id, set from the hook's payload
// even when the process environment names none.
func TestAppendEvent_AHookWithNoAgentRecordsTheSessionOnly(t *testing.T) {
	isolateEvents(t)
	t.Setenv("CLAUDE_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	main, _ := laneRoot(t, "lane/x")
	SetHookActor("sid-2", "")
	t.Cleanup(func() { SetHookActor("", "") })

	AppendEvent(Event{Kind: "commit_gate", Root: main, Stage: "precommit"})

	if got := actorOfLastEvent(t, main); got != "sid-2" {
		t.Errorf("actor = %q, want sid-2", got)
	}
}

// A git hook has no session: its events stay as they are.
func TestAppendEvent_AGitHookWithNoSessionStaysUnattributed(t *testing.T) {
	isolateEvents(t)
	t.Setenv("CLAUDE_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	main, _ := laneRoot(t, "lane/x")

	AppendEvent(Event{Kind: "commit_gate", Root: main, Stage: "precommit"})

	if got := actorOfLastEvent(t, main); got != "" {
		t.Errorf("actor = %q, want none", got)
	}
}
