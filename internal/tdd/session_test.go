package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// promptJSON builds a UserPromptSubmit payload.
func promptJSON(prompt, session, cwd string) []byte {
	b, _ := json.Marshal(promptInput{Prompt: prompt, SessionID: session, Cwd: cwd})
	return b
}

func TestHandlePrompt_OffOnToggle(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-1"

	// /tdd off persists the override.
	r := HandlePrompt(promptJSON("/tdd off", sess, ""))
	if !r.Block || !strings.Contains(r.Message, "OFF") {
		t.Fatalf("/tdd off: got %+v", r)
	}
	if s, _ := loadSession(sess); s == nil || !s.Overrides.Off {
		t.Fatal("/tdd off did not persist Overrides.Off")
	}

	// /tdd on clears it.
	r = HandlePrompt(promptJSON("/tdd on", sess, ""))
	if !r.Block || !strings.Contains(r.Message, "ON") {
		t.Fatalf("/tdd on: got %+v", r)
	}
	if s, _ := loadSession(sess); s == nil || s.Overrides.Off {
		t.Fatal("/tdd on did not clear Overrides.Off")
	}
}

func TestHandlePrompt_Status(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := HandlePrompt(promptJSON("/tdd status", "sess-2", ""))
	if !r.Block || !strings.Contains(r.Message, "enforcement") {
		t.Fatalf("/tdd status: got %+v", r)
	}
	// Bare /tdd is an alias for status.
	if r := HandlePrompt(promptJSON("/tdd", "sess-2", "")); !r.Block {
		t.Fatalf("bare /tdd should block with status: got %+v", r)
	}
	// Unknown subcommand is reported, still blocking.
	r = HandlePrompt(promptJSON("/tdd frobnicate", "sess-2", ""))
	if !r.Block || !strings.Contains(r.Message, "unknown subcommand") {
		t.Fatalf("/tdd frobnicate: got %+v", r)
	}
}

// stampOutcome records an outcome for root in a session, for reinforcement tests.
func stampOutcome(t *testing.T, sess, root, outcome string) {
	t.Helper()
	s, path := loadSession(sess)
	if s == nil {
		t.Fatal("loadSession returned nil")
	}
	s.Stamp(root, projectState{Outcome: outcome})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}

func TestHandlePrompt_ReinforceRedOnly(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-3"
	root := t.TempDir()
	// Make the cwd resolve to a project root.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A RED outcome is re-injected on an ordinary prompt.
	stampOutcome(t, sess, root, string(RedMissingImpl))
	if r := HandlePrompt(promptJSON("keep working", sess, root)); r.Block || !strings.Contains(r.Message, "RED") {
		t.Fatalf("RED reinforcement: got %+v", r)
	}

	// A green outcome stays silent.
	stampOutcome(t, sess, root, string(Green))
	if r := HandlePrompt(promptJSON("keep working", sess, root)); r.Message != "" {
		t.Fatalf("green must be silent: got %+v", r)
	}

	// Enforcement off silences reinforcement even when RED.
	stampOutcome(t, sess, root, string(Red))
	if err := setOff(sess, true); err != nil {
		t.Fatal(err)
	}
	if r := HandlePrompt(promptJSON("keep working", sess, root)); r.Message != "" {
		t.Fatalf("off must silence reinforcement: got %+v", r)
	}
}

func TestRenderPrompt(t *testing.T) {
	// A blocking command renders a deny envelope, exit 0.
	body, code := RenderPrompt(PromptResult{Block: true, Message: "off"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var out promptOutput
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if out.Decision != "block" || out.HookSpecificOutput.AdditionalContext != "off" {
		t.Fatalf("unexpected envelope: %+v", out)
	}
	// An empty result is silent.
	if body, code := RenderPrompt(PromptResult{}); body != nil || code != 0 {
		t.Fatalf("empty = (%q, %d), want (nil, 0)", body, code)
	}
}

func TestEndSession_RemovesStateFile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-4"
	// Create a state file.
	if err := setOff(sess, true); err != nil {
		t.Fatal(err)
	}
	_, path := loadSession(sess)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file should exist: %v", err)
	}

	EndSession(mustJSON(t, sessionEndInput{SessionID: sess}))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state file should be gone, stat err = %v", err)
	}
	// No session id is a safe no-op.
	EndSession(mustJSON(t, sessionEndInput{}))
}

// TestEndSession_KillsADeferredJobStillRunningForThatSession pins the reap:
// a phase left running when the session ends has no later hook coming to
// harvest it (loadDeferredJob is only ever looked up by the CURRENT
// session's own id), so without this it would run — and hold its build
// slot — forever. EndSession must kill it and drop its record before it
// drops the session's own state file.
func TestEndSession_KillsADeferredJobStillRunningForThatSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-wedged"
	root := t.TempDir()
	saveDeferredJob(DeferredJob{
		Project: root, Session: sess, Phase: "run", Dir: root, PID: 5150,
		Runner: []string{"cargo", "test"}, Started: time.Now(),
	})

	var killed []int
	prev := killDeferredFn
	killDeferredFn = func(j DeferredJob) { killed = append(killed, j.PID) }
	t.Cleanup(func() { killDeferredFn = prev })

	EndSession(mustJSON(t, sessionEndInput{SessionID: sess}))

	if len(killed) != 1 || killed[0] != 5150 {
		t.Fatalf("killed = %v, want exactly [5150]", killed)
	}
	if _, ok := loadDeferredJob(sess, root); ok {
		t.Fatal("the reaped job's record must be gone")
	}
}

// TestEndSession_LeavesAnotherSessionsDeferredJobRunning pins the scope: a
// session ending must reap only what IT started, never a phase a different,
// still-live session left running in the same project.
func TestEndSession_LeavesAnotherSessionsDeferredJobRunning(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-other", Phase: "run", Dir: root, PID: 6161,
		Runner: []string{"cargo", "test"}, Started: time.Now(),
	})

	var killed []int
	prev := killDeferredFn
	killDeferredFn = func(j DeferredJob) { killed = append(killed, j.PID) }
	t.Cleanup(func() { killDeferredFn = prev })

	EndSession(mustJSON(t, sessionEndInput{SessionID: "sess-ending"}))

	if len(killed) != 0 {
		t.Fatalf("killed = %v, want none — that job belongs to a different session", killed)
	}
	if _, ok := loadDeferredJob("sess-other", root); !ok {
		t.Fatal("another session's still-running job must survive")
	}
}

// The reply-style block (internal/tdd/style.md) rides in the payload's
// additionalContext on every ordinary prompt by default — the gate's
// replacement for the third-party plugin that used to inject it.
func TestHandlePrompt_StyleAppendedByDefault(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	body, _ := RenderPrompt(HandlePrompt(promptJSON("keep working", "sess-style-1", "")))
	var out promptOutput
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("payload not JSON: %v (body=%q)", err, body)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "Reply style: terse") {
		t.Fatalf("additionalContext missing the style block: %q", out.HookSpecificOutput.AdditionalContext)
	}
}

// `/tdd style plain` silences the block on every later prompt in the session.
func TestHandlePrompt_StyleSilentWhenPlain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-style-2"
	r := HandlePrompt(promptJSON("/tdd style plain", sess, ""))
	if !r.Block || !strings.Contains(r.Message, "plain") {
		t.Fatalf("/tdd style plain: got %+v", r)
	}
	// An ordinary prompt in the same session now carries no style block, and
	// with nothing else to say the hook stays fully silent (nil payload).
	body, code := RenderPrompt(HandlePrompt(promptJSON("keep working", sess, "")))
	if body != nil || code != 0 {
		t.Fatalf("plain style should leave a quiet prompt silent, got body=%q code=%d", body, code)
	}
}

// `/tdd style terse|plain` round-trips through session state, and `/tdd
// status` reports the current setting.
func TestTddCommand_StyleRoundTripsThroughState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "sess-style-3"
	if r := HandlePrompt(promptJSON("/tdd style plain", sess, "")); !strings.Contains(r.Message, "plain") {
		t.Fatalf("/tdd style plain: got %+v", r)
	}
	if s, _ := loadSession(sess); s == nil || s.Overrides.Style != "plain" {
		t.Fatal("/tdd style plain did not persist Overrides.Style")
	}
	if r := HandlePrompt(promptJSON("/tdd status", sess, "")); !strings.Contains(r.Message, "plain") {
		t.Fatalf("/tdd status should report the style: got %+v", r)
	}
	if r := HandlePrompt(promptJSON("/tdd style terse", sess, "")); !strings.Contains(r.Message, "terse") {
		t.Fatalf("/tdd style terse: got %+v", r)
	}
	if s, _ := loadSession(sess); s == nil || s.Overrides.Style != "terse" {
		t.Fatal("/tdd style terse did not persist Overrides.Style")
	}
	// An unrecognised style argument is reported, not silently accepted.
	r := HandlePrompt(promptJSON("/tdd style loud", sess, ""))
	if !r.Block || !strings.Contains(r.Message, "terse or plain") {
		t.Fatalf("/tdd style loud: got %+v", r)
	}
}

// SessionStart includes the style block once, in the same payload as the
// skill nudge, so it survives a later context compaction that would drop it
// otherwise.
func TestHandleSessionStart_IncludesStyleBlockOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	msg := HandleSessionStart([]byte(`{"session_id":"ss-style"}`))
	if strings.Count(msg, "Reply style: terse") != 1 {
		t.Fatalf("session-start nudge should carry the style block exactly once:\n%s", msg)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
