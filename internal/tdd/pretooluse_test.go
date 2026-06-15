package tdd

import (
	"encoding/json"
	"testing"
)

func decide(t *testing.T, payload string) Decision {
	t.Helper()
	d, err := DecidePreEdit([]byte(payload))
	if err != nil {
		t.Fatalf("DecidePreEdit(%s): %v", payload, err)
	}
	return d
}

func TestDecidePreEdit(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    Action
	}{
		{
			name:    "non-edit tool allowed",
			payload: `{"tool_name":"Bash","tool_input":{"command":"it.only()"}}`,
			want:    Allow,
		},
		{
			name:    "source edit flows even with a smell",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"time.Sleep(2)"}}`,
			want:    Allow,
		},
		{
			name:    "test edit with sleep blocks",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget_test.go","new_string":"time.Sleep(2)"}}`,
			want:    Block,
		},
		{
			name:    "clean test edit flows",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"src/widget_test.go","content":"assert x == y"}}`,
			want:    Allow,
		},
		{
			name:    "tautology in test blocks",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"a.test.ts","content":"expect(x).toBe(x)"}}`,
			want:    Block,
		},
		{
			name:    "MultiEdit edits[] are inspected",
			payload: `{"tool_name":"MultiEdit","tool_input":{"file_path":"a_test.py","edits":[{"new_string":"assert x == x"}]}}`,
			want:    Block,
		},
		{
			name:    "edit without a path is allowed",
			payload: `{"tool_name":"Edit","tool_input":{"new_string":"it.only()"}}`,
			want:    Allow,
		},
		{
			name:    "disabled test blocks",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"a.test.ts","content":"it.skip('x', () => {})"}}`,
			want:    Block,
		},
		{
			name:    "go t.Skip in test blocks",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"w_test.go","new_string":"func TestX(t *testing.T){ t.Skip() }"}}`,
			want:    Block,
		},
		{
			name:    "suppression in SOURCE file warns (not block)",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"x := f() //nolint:errcheck"}}`,
			want:    Warn,
		},
		{
			name:    "suppression in TEST file warns (smells would block, this is just a warn)",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"a.test.ts","content":"const x = y // @ts-ignore"}}`,
			want:    Warn,
		},
		{
			name:    "clean source edit allowed",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"return x + 1"}}`,
			want:    Allow,
		},
		{
			name:    "suppression quoted in a string does not trip",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"msg := \"use //nolint to skip\""}}`,
			want:    Allow,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decide(t, c.payload).Action; got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestDecidePreEdit_ParseError(t *testing.T) {
	if _, err := DecidePreEdit([]byte("{not json")); err == nil {
		t.Fatal("expected a parse error for malformed JSON")
	}
}

func TestRenderPreToolUse(t *testing.T) {
	// Block renders a deny envelope and exits 2.
	body, code := RenderPreToolUse(Decision{Action: Block, Reason: "bad"})
	if code != 2 {
		t.Fatalf("block exit = %d, want 2", code)
	}
	var out preToolUseOutput
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("block payload not JSON: %v", err)
	}
	if out.Decision != "block" || out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("unexpected block envelope: %+v", out)
	}

	// Allow is silent: no payload, exit 0.
	if body, code := RenderPreToolUse(Decision{Action: Allow}); body != nil || code != 0 {
		t.Fatalf("allow = (%q, %d), want (nil, 0)", body, code)
	}
}
