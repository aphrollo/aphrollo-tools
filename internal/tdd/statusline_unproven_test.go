package tdd

import (
	"strings"
	"testing"
	"time"
)

// Green was the badge's fallback for everything that was not a standing red or
// a running job, so a session that had never recorded an outcome, and a
// session whose only red had gone stale, both read exactly like a session
// whose suite had just passed. That is the same shape as failing open: the
// absence of a measurement rendered as a good one.
//
// It compounds with the deferred pipeline. When a post-edit job is abandoned
// no outcome is written at all, so nothing ever turns the badge red and green
// is what a session sees while its edits go untested. The badge is armed
// either way, so the colour stays green; what changes is that it stops
// claiming a verdict it does not have.

func TestStatusState_SaysUnprovenWhenNoOutcomeWasEverRecorded(t *testing.T) {
	root := statusRoot(t)

	colour, tag := statusState("session-with-no-history", root)

	if colour != ansiGreen {
		t.Errorf("colour = %q, want green — the gate is armed, and that part is true", colour)
	}
	if tag != tagUnproven {
		t.Errorf("tag = %q, want %q — a session that has recorded nothing has not been measured, and a "+
			"bare green badge says the suite passed", tag, tagUnproven)
	}
}

func TestStatusState_SaysUnprovenWhenTheOnlyRedWentStale(t *testing.T) {
	root := statusRoot(t)
	session := "session-with-a-stale-red"
	stampOutcomeAt(t, session, root, string(Red), time.Now().Add(-2*redGoesStaleAfter))

	colour, tag := statusState(session, root)

	if colour != ansiGreen {
		t.Errorf("colour = %q, want green — a red nobody has re-measured is not a red", colour)
	}
	if tag != tagUnproven {
		t.Errorf("tag = %q, want %q — the tree was last seen RED and nothing has measured it since, "+
			"which is the one state that must never render as a pass", tag, tagUnproven)
	}
}

func TestStatusState_StaysBareGreenOnARecordedGreen(t *testing.T) {
	root := statusRoot(t)
	session := "session-with-a-green"
	stampOutcomeAt(t, session, root, string(Green), time.Now())

	colour, tag := statusState(session, root)

	if colour != ansiGreen || tag != "" {
		t.Errorf("badge = %q/%q, want a bare green — a suite that actually passed is the one case the "+
			"colour alone is allowed to carry", colour, tag)
	}
}

func TestBadge_UnprovenSurvivesAColourStrippingConsumer(t *testing.T) {
	got := badge(ansiGreen, tagUnproven)
	if !strings.Contains(got, tagUnproven) {
		t.Errorf("badge = %q, want the tag in the text — a consumer that strips colour must not read "+
			"an unmeasured tree as a passing one", got)
	}
}
