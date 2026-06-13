package guardrail

import (
	"strings"
	"testing"
)

func TestDecideFromHookInput(t *testing.T) {
	block := `{"tool_name":"Bash","tool_input":{"command":"sleep 600"}}`
	if d, err := DecideFromHookInput([]byte(block)); err != nil || d.Action != Block {
		t.Fatalf("block input: action=%v err=%v, want Block", d.Action, err)
	}

	allow := `{"tool_name":"Edit","tool_input":{"file_path":"x.go"}}`
	if d, err := DecideFromHookInput([]byte(allow)); err != nil || d.Action != Allow {
		t.Fatalf("edit input: action=%v err=%v, want Allow", d.Action, err)
	}

	if _, err := DecideFromHookInput([]byte("{not json")); err == nil {
		t.Fatalf("malformed input: want error, got nil")
	}
}

func TestRender_Block(t *testing.T) {
	payload, code := Render(Decision{Action: Block, Reason: "too long"})
	if code != 2 {
		t.Fatalf("block exit code = %d, want 2", code)
	}
	s := string(payload)
	if !strings.Contains(s, `"decision":"block"`) || !strings.Contains(s, `"permissionDecision":"deny"`) {
		t.Fatalf("block payload missing deny envelope: %s", s)
	}
	if !strings.Contains(s, "too long") {
		t.Fatalf("block payload missing reason: %s", s)
	}
}

func TestRender_Warn(t *testing.T) {
	payload, code := Render(Decision{Action: Warn, Reason: "use -q"})
	if code != 0 {
		t.Fatalf("warn exit code = %d, want 0 (non-blocking)", code)
	}
	s := string(payload)
	if !strings.Contains(s, "additionalContext") || !strings.Contains(s, "use -q") {
		t.Fatalf("warn payload missing additionalContext/reason: %s", s)
	}
	if strings.Contains(s, `"decision":"block"`) {
		t.Fatalf("warn must not block: %s", s)
	}
}

func TestRender_Allow_IsSilent(t *testing.T) {
	payload, code := Render(Decision{Action: Allow})
	if code != 0 {
		t.Fatalf("allow exit code = %d, want 0", code)
	}
	if len(payload) != 0 {
		t.Fatalf("allow should emit nothing, got: %s", payload)
	}
}
