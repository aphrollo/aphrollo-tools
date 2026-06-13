package guardrail

import (
	"strings"
	"testing"
)

func TestEvaluate_WarnsOnNoisyCommands(t *testing.T) {
	cases := []struct {
		command    string
		warn       bool
		suggestSub string // substring expected in the suggestion when warning
	}{
		{"pytest tests/", true, "-q"},
		{"pytest -q tests/", false, ""},         // already quiet
		{"pytest tests/ | tail -50", false, ""}, // output already bounded
		{"cargo build", true, "-q"},
		{"cargo build --quiet", false, ""},
		{"npm install", true, "silent"},
		{"ls -la", false, ""},
	}
	for _, c := range cases {
		d := Evaluate("Bash", c.command)
		gotWarn := d.Action == Warn
		if gotWarn != c.warn {
			t.Fatalf("Evaluate(Bash, %q).Action = %v, want warn=%v", c.command, d.Action, c.warn)
		}
		if c.warn && !strings.Contains(strings.ToLower(d.Reason), c.suggestSub) {
			t.Fatalf("Evaluate(Bash, %q) warn reason = %q, want substring %q", c.command, d.Reason, c.suggestSub)
		}
	}
}

// A blocking wait still takes precedence over a noisy-output warning.
func TestEvaluate_BlockBeatsWarn(t *testing.T) {
	if d := Evaluate("Bash", "pytest tests/ && sleep 600"); d.Action != Block {
		t.Fatalf("expected Block to win over Warn, got %v", d.Action)
	}
}
