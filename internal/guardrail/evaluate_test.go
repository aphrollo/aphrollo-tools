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
		{"sleep 5", true}, // a dispatch turn should not idle for seconds
		{"sleep 2", true}, // threshold: >= 2s blocks
		{"sleep 10m", true},
		{"sleep 1", false}, // brief pacing under 2s is allowed
		{"sleep 0.5", false},
		{"sleep 1.5", false},
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

// Blocking watch/follow commands idle a dispatch turn just as badly as a long
// sleep, so they are blocked with the same poll-loop/background fix hint.
func TestEvaluate_BlocksBlockingWatch(t *testing.T) {
	cases := []struct {
		command string
		block   bool
	}{
		{"gh pr checks 308 --watch --interval 20", true},
		{"gh pr checks 308 --watch", true},
		{"tail -f /tmp/foo", true},
		{"tail --follow=name /var/log/syslog", true},
		{"journalctl -f -u aphrollo-api", true},
		{"journalctl -u aphrollo-api --follow", true},
		{"watch gh pr checks 308", true},
		{"watch -n5 ls", true},
		// A bounded poll loop is the blessed form — sub-threshold sleep, no
		// --watch/-f, so neither the watch nor the sleep block must misfire.
		{"until gh pr checks 308; do sleep 1; done", false},
		{"gh pr checks 308", false},     // a one-shot check is fine
		{"tail -n 50 /tmp/foo", false},  // bounded tail is fine
		{"git stopwatch", false},        // not the watch(1) command
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

// A watch/follow command mentioned inside a quoted string or comment is not a
// real blocking wait and must not be blocked.
func TestEvaluate_IgnoresWatchInStringsAndComments(t *testing.T) {
	allowed := []string{
		`echo "gh pr checks 308 --watch"`,
		`git commit -m "tail -f log helper"`,
		`echo hi # then journalctl -f later`,
	}
	for _, cmd := range allowed {
		if d := Evaluate("Bash", cmd); d.Action == Block {
			t.Fatalf("Evaluate(Bash, %q) blocked, want not-blocked (watch is quoted/commented)", cmd)
		}
	}
}

// The blocking-watch block, like the sleep block, must point at a concrete fix.
func TestEvaluate_BlockingWatchReasonSuggestsFix(t *testing.T) {
	d := Evaluate("Bash", "gh pr checks 308 --watch")
	if d.Action != Block {
		t.Fatalf("expected Block, got %v", d.Action)
	}
	r := strings.ToLower(d.Reason)
	if !strings.Contains(r, "until") && !strings.Contains(r, "background") {
		t.Fatalf("block reason should suggest a poll/background fix, got: %s", d.Reason)
	}
}

// The sleep block's recommended poll loop must use a sub-threshold sleep so the
// fix it suggests does not itself trip the block (sleep 2 == blockSleepAtSeconds).
func TestEvaluate_LongSleepFixIsNotSelfBlocking(t *testing.T) {
	d := Evaluate("Bash", "sleep 600")
	if d.Action != Block {
		t.Fatalf("expected Block, got %v", d.Action)
	}
	if strings.Contains(d.Reason, "sleep 2") {
		t.Fatalf("sleep block reason recommends sleep 2, which is itself blocked: %s", d.Reason)
	}
}

// Non-Bash tools are never the guardrail's concern.
func TestEvaluate_IgnoresNonBash(t *testing.T) {
	if d := Evaluate("Edit", "sleep 600"); d.Action != Allow {
		t.Fatalf("Edit tool should be allowed regardless of args, got %v", d.Action)
	}
}

// A `sleep` mentioned inside a quoted string or a comment is not a real
// command and must not be blocked — a false block would wedge the agent.
func TestEvaluate_IgnoresSleepInStringsAndComments(t *testing.T) {
	allowed := []string{
		`echo "sleep 600"`,
		`echo 'sleep 600'`,
		`git commit -m "add sleep 600 helper"`,
		`echo hi # then sleep 600 later`,
	}
	for _, cmd := range allowed {
		if d := Evaluate("Bash", cmd); d.Action == Block {
			t.Fatalf("Evaluate(Bash, %q) blocked, want not-blocked (sleep is quoted/commented)", cmd)
		}
	}

	// A real sleep, including inside command substitution, still blocks.
	for _, cmd := range []string{`sleep 600`, `echo $(sleep 600)`} {
		if d := Evaluate("Bash", cmd); d.Action != Block {
			t.Fatalf("Evaluate(Bash, %q) = %v, want Block", cmd, d.Action)
		}
	}
}

// Noisy-command detection also ignores matches inside strings.
func TestEvaluate_IgnoresNoisyToolInString(t *testing.T) {
	if d := Evaluate("Bash", `echo "run pytest tests/ now"`); d.Action != Allow {
		t.Fatalf("quoted pytest mention should be allowed, got %v", d.Action)
	}
}
