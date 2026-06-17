package tdd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleSessionStart_NudgesSkills(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	msg := HandleSessionStart([]byte(`{"session_id":"ss-1"}`))
	for _, want := range []string{"test-driven-development", "incremental-implementation", "SKILL.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("session-start nudge missing %q:\n%s", want, msg)
		}
	}
}

func TestHandleSessionStart_SilentWhenOff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const sess = "ss-off"
	if err := setOff(sess, true); err != nil {
		t.Fatal(err)
	}
	if msg := HandleSessionStart([]byte(`{"session_id":"` + sess + `"}`)); msg != "" {
		t.Fatalf("nudge should be silent when TDD enforcement is off, got %q", msg)
	}
}

func TestRenderSessionStart(t *testing.T) {
	body, code := RenderSessionStart("hello")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q, want SessionStart", out.HookSpecificOutput.HookEventName)
	}
	if out.HookSpecificOutput.AdditionalContext != "hello" {
		t.Fatalf("additionalContext = %q, want hello", out.HookSpecificOutput.AdditionalContext)
	}
	if b, c := RenderSessionStart(""); b != nil || c != 0 {
		t.Fatalf("empty nudge should be silent, got body=%q code=%d", b, c)
	}
}
