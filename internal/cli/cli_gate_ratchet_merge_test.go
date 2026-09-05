package cli

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestMergeRatchetAdvisory_KeepsBothMessagesOnATie pins issue #296: the old
// `if r.Action > decision.Action { decision = r }` only ever fired on a
// STRICT escalation, so a warn-severity ratchet law hit landing beside an
// already-Warn DecidePreEdit verdict (a suppression note, a test-quality
// note) discarded the ratchet's law name, location and remedy entirely — the
// commit-time stage still enforced the law, but the edit-time signal was
// gone. A tie must keep both.
func TestMergeRatchetAdvisory_KeepsBothMessagesOnATie(t *testing.T) {
	decision := tdd.Decision{Action: tdd.Warn, Reason: "pre-edit: suppression note", Policy: "suppression"}
	ratchet := tdd.Decision{Action: tdd.Warn, Reason: "ratchet: comment_hygiene hit at line 4", Policy: "ratchet:comment_hygiene"}

	got := mergeRatchetAdvisory(decision, ratchet)

	if got.Action != tdd.Warn {
		t.Fatalf("Action = %v, want Warn preserved on a tie", got.Action)
	}
	if !strings.Contains(got.Reason, decision.Reason) {
		t.Fatalf("Reason = %q, lost the pre-edit decision's own message", got.Reason)
	}
	if !strings.Contains(got.Reason, ratchet.Reason) {
		t.Fatalf("Reason = %q, lost the ratchet law's message — issue #296", got.Reason)
	}
}

// TestMergeRatchetAdvisory_EscalatesOnAStrictlyGreaterAction pins the other
// half: a ratchet Block over a pre-edit Allow/Warn still wins outright
// (unchanged behaviour — a deny law denies the write before it lands).
func TestMergeRatchetAdvisory_EscalatesOnAStrictlyGreaterAction(t *testing.T) {
	decision := tdd.Decision{Action: tdd.Warn, Reason: "pre-edit: suppression note"}
	ratchet := tdd.Decision{Action: tdd.Block, Reason: "ratchet: deny-law hit"}

	got := mergeRatchetAdvisory(decision, ratchet)

	if got.Action != tdd.Block || got.Reason != ratchet.Reason {
		t.Fatalf("got %+v, want the ratchet Block verdict to replace the pre-edit Warn outright", got)
	}
}
