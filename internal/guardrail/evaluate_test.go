package guardrail

import (
	"strings"
	"testing"
)

func TestEvaluate_BlocksLongSleep(t *testing.T) {
	cases := []struct {
		command string
		block   bool
	}{
		{"sleep 600", true},
		{"sleep 301", true},
		{"sleep 10m", true},  // 600s
		{"sleep 0.5h", true}, // 1800s
		{"sleep 300", false}, // boundary: not > 300
		{"sleep 5", false},
		{"echo hi && sleep 900", true}, // nested in a compound command
		{"ls -la", false},
	}
	for _, c := range cases {
		d := Evaluate("Bash", c.command)
		if (d.Action == Block) != c.block {
			t.Fatalf("Evaluate(Bash, %q).Action = %v, want block=%v", c.command, d.Action, c.block)
		}
		if c.block && d.Reason == "" {
			t.Fatalf("Evaluate(Bash, %q) blocked without a reason", c.command)
		}
	}
}

func TestEvaluate_LongSleepReasonSuggestsFix(t *testing.T) {
	d := Evaluate("Bash", "sleep 600")
	if d.Action != Block {
		t.Fatalf("expected Block, got %v", d.Action)
	}
	r := strings.ToLower(d.Reason)
	if !strings.Contains(r, "poll") && !strings.Contains(r, "background") {
		t.Fatalf("block reason should suggest a poll/background fix, got: %s", d.Reason)
	}
}

// Non-Bash tools are never the guardrail's concern.
func TestEvaluate_IgnoresNonBash(t *testing.T) {
	if d := Evaluate("Edit", "sleep 600"); d.Action != Allow {
		t.Fatalf("Edit tool should be allowed regardless of args, got %v", d.Action)
	}
}
