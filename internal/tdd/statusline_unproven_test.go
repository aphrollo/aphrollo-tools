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
// is what a session sees while its edits go untested.
//
// The unmeasured state is a COLOUR, not a word: white for a gate that is on
// with nothing measured, gray for a gate that is off, green kept for a suite
// that actually passed. A tag on every render is noise a reader stops seeing,
// and "unproven" describes the common case -- most renders happen between
// suites, not after one.

// ratchet: test_removed TestStatusState_SaysUnprovenWhenNoOutcomeWasEverRecorded: renamed,
// not deleted. Same case, same seed -- what it asserts is now a colour
// rather than a tag.
func TestStatusState_IsWhiteWhenNoOutcomeWasEverRecorded(t *testing.T) {
	root := statusRoot(t)

	colour, tag := statusState("session-with-no-history", root)

	if colour != ansiWhite {
		t.Errorf("colour = %q, want white — the gate is on, and nothing has measured this tree", colour)
	}
	if tag != "" {
		t.Errorf("tag = %q, want none — the colour carries the state", tag)
	}
}

// ratchet: test_removed TestStatusState_SaysUnprovenWhenTheOnlyRedWentStale: renamed,
// not deleted. The stale red is still dropped; what it drops to is
// white now, not a green carrying a tag.
func TestStatusState_IsWhiteWhenTheOnlyRedWentStale(t *testing.T) {
	root := statusRoot(t)
	session := "session-with-a-stale-red"
	stampOutcomeAt(t, session, root, string(Red), time.Now().Add(-2*redGoesStaleAfter))

	colour, tag := statusState(session, root)

	if colour != ansiWhite {
		t.Errorf("colour = %q, want white — the tree was last seen RED and nothing has measured it "+
			"since, which is the one state that must never render as a pass", colour)
	}
	if tag != "" {
		t.Errorf("tag = %q, want none", tag)
	}
}

func TestStatusState_IsWhiteWhenTheSessionStandsOutsideAnyRoot(t *testing.T) {
	colour, tag := statusState("session-with-no-history", t.TempDir())

	if colour != ansiWhite || tag != "" {
		t.Errorf("badge = %q/%q, want a bare white — no root means no measurement, which is not a pass",
			colour, tag)
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

// ratchet: test_removed TestBadge_UnprovenSurvivesAColourStrippingConsumer: the
// `unproven` tag it guarded is gone -- the unmeasured state is now white, and a
// consumer that strips colour reads it as the plain armed badge. Only OFF still
// carries a word, because reading an ungated tree as gated is the one mistake
// the badge must not enable; an unmeasured armed tree read as armed is not.
func TestBadge_RendersWhiteBare(t *testing.T) {
	got := badge(ansiWhite, "")
	if !strings.Contains(got, ansiWhite) || !strings.Contains(got, badgeOn) {
		t.Errorf("badge = %q, want the white armed badge", got)
	}
	if strings.Contains(got, ":") {
		t.Errorf("badge = %q, want no tag — white already says unmeasured", got)
	}
}
