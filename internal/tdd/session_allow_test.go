package tdd

import (
	"strings"
	"testing"
)

// The /tdd session verb must accept every wall `aphrollo gate allow` knows.
func TestTddAllow_AcceptsEveryWallTheCLIKnows(t *testing.T) {
	for _, wall := range []string{"primary", "source-bash"} {
		t.Run(wall, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			t.Setenv("CLAUDE_SESSION_ID", "s-allow-"+wall)

			msg := tddCommand("allow", wall, "s-allow-"+wall, "")
			if strings.HasPrefix(msg, "gate:") {
				t.Fatalf("/tdd allow %s refused: %q", wall, msg)
			}
			if !Waived(wall) {
				t.Fatalf("/tdd allow %s did not waive the wall", wall)
			}
			msg = tddCommand("revoke", wall, "s-allow-"+wall, "")
			if strings.HasPrefix(msg, "gate:") {
				t.Fatalf("/tdd revoke %s refused: %q", wall, msg)
			}
			if Waived(wall) {
				t.Fatalf("/tdd revoke %s left the wall waived", wall)
			}
		})
	}
}

// discard is a one-shot arm, not a session waiver: the verb must arm it the
// way `gate allow discard` does.
func TestTddAllow_ArmsDiscardForOneCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-allow-discard")

	msg := tddCommand("allow", "discard", "s-allow-discard", "")
	if !strings.HasPrefix(msg, "Discard ARMED for one command") {
		t.Fatalf("/tdd allow discard = %q, want the one-shot arm message", msg)
	}
	if !ConsumeOneShot(WallDiscard) {
		t.Fatal("/tdd allow discard did not arm a consumable one-shot")
	}
}

func TestTddAllow_RefusesAnUnknownWallNamingEveryKnownOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	for _, verb := range []string{"allow", "revoke"} {
		msg := tddCommand(verb, "nonsense", "s-unknown", "")
		want := "gate: /tdd " + verb + " needs a wall (primary|discard|source-bash), got nonsense"
		if msg != want {
			t.Fatalf("/tdd %s nonsense = %q, want %q", verb, msg, want)
		}
	}
}
