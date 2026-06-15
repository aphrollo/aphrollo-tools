package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	s.stamp(root, projectState{Outcome: outcome})
	if err := s.save(path); err != nil {
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

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
