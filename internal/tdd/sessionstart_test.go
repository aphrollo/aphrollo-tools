package tdd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHandleSessionStart_NudgesSkills pins the CONTRACT the nudge must state
// (updated 2026-08-15, build-infra-fix task A2), not its exact wording: it
// names an existing skill (superpowers:test-driven-development —
// "incremental-implementation" does not exist on this box and was a dead
// reference), and it says the hooks — not the model — run the tests, so a
// session stops re-running suites by hand the loud gates already ran and
// reported. Checking behaviorally important phrases rather than the full
// string keeps this from being a change detector on prose.
func TestHandleSessionStart_NudgesSkills(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	msg := HandleSessionStart([]byte(`{"session_id":"ss-1"}`))
	for _, want := range []string{"superpowers:test-driven-development", "hooks run the tests", "TIMEOUT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("session-start nudge missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "incremental-implementation") {
		t.Errorf("nudge must not reference the nonexistent incremental-implementation skill:\n%s", msg)
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
