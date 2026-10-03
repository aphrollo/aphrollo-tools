package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopes_clipOnlyPastTheCapToTheByte(t *testing.T) {
	text := func(raw []byte) string {
		var v struct {
			Out struct {
				Context string `json:"additionalContext"`
				Reason  string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return v.Out.Context + v.Out.Reason + v.Reason
	}
	cases := []struct {
		name  string
		bytes int
		put   func(string) []byte
	}{
		{"PreToolUse guide", 240, func(s string) []byte { return Context(HookPreToolUse, s) }},
		{"SubagentStart", 1000, func(s string) []byte { return Context(HookSubagentStart, s) }},
		{"SessionStart", 1600, func(s string) []byte { return Context(HookSessionStart, s) }},
		{"PostToolBatch", PlatformContextBytes, func(s string) []byte { return Context(HookPostToolBatch, s) }},
		{"deny", 480, PreToolUseDeny},
		{"allow", 240, PreToolUseAllow},
		{"stop block", 1600, StopBlock},
	}
	for _, c := range cases {
		fits := strings.Repeat("a", c.bytes)
		if got := text(c.put(fits)); got != fits {
			t.Errorf("%s: a text of exactly %d bytes was changed", c.name, c.bytes)
		}
		if got := text(c.put(fits + "a")); !strings.HasSuffix(got, "cut: text") || len(got) > c.bytes {
			t.Errorf("%s: a text one byte over was not cut to %d bytes and named: %d bytes", c.name, c.bytes, len(got))
		}
	}
}
