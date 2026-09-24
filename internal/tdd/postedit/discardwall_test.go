package postedit

import (
	"strings"
	"testing"
	"time"
)

// The discard wall's waiver is one-shot, unlike the primary wall's
// session-long one: `gate allow discard` arms it for a single command, not
// for the rest of the session, so a lane that means to discard exactly one
// thing does not leave the wall open behind it.

func TestAllowDiscard_ArmsOneShotAndListsAsArmedUntil(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const session = "s-allow-discard"
	t.Setenv("CLAUDE_SESSION_ID", session)

	msg, err := AllowWall(WallDiscard)
	if err != nil {
		t.Fatalf("AllowWall(discard): %v", err)
	}
	if !strings.HasPrefix(msg, "Discard ARMED for one command in this session (until ") {
		t.Fatalf("message = %q, want it to open with the ARMED sentence", msg)
	}
	if !strings.HasSuffix(msg, "). Run `aphrollo gate revoke discard` to disarm.") {
		t.Fatalf("message = %q, want it to name the disarm command", msg)
	}

	ws := ListWaivers()
	if len(ws) != 1 {
		t.Fatalf("ListWaivers() = %v, want exactly one entry", ws)
	}
	w := ws[0]
	if w.Wall != WallDiscard || w.Session != session {
		t.Fatalf("waiver = %+v, want wall=%q session=%q", w, WallDiscard, session)
	}
	if w.Until.IsZero() {
		t.Fatal("an armed waiver's Until must not be zero")
	}
}

// A one-shot arm is spent by the FIRST command that checks it, not by every
// command for the rest of the session — the second attempt is refused again.
func TestConsumeOneShot_TrueOnceThenFalse(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-consume-once")

	if _, err := AllowWall(WallDiscard); err != nil {
		t.Fatal(err)
	}
	if !ConsumeOneShot(WallDiscard) {
		t.Fatal("the first consumption of an armed waiver must succeed")
	}
	if ConsumeOneShot(WallDiscard) {
		t.Fatal("a second consumption must find nothing armed")
	}
	if ws := ListWaivers(); len(ws) != 0 {
		t.Fatalf("ListWaivers() after consuming = %v, want none", ws)
	}
}

func TestConsumeOneShot_FalseWhenNeverArmed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-never-armed")

	if ConsumeOneShot(WallDiscard) {
		t.Fatal("ConsumeOneShot must report false when the wall was never armed")
	}
}

// An arm nobody consumed within 5 minutes is spent anyway: a session that
// armed it and then never ran the command it meant to must not leave the
// wall open for something unrelated much later.
func TestConsumeOneShot_ExpiresAfterFiveMinutes(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-expires")

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	now := t0
	restore := SetDiscardClockForTest(func() time.Time { return now })
	defer restore()

	if _, err := AllowWall(WallDiscard); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(5*time.Minute + time.Second)
	if ConsumeOneShot(WallDiscard) {
		t.Fatal("an arm older than 5 minutes must have expired")
	}
	if ws := ListWaivers(); len(ws) != 0 {
		t.Fatalf("ListWaivers() after an expired consumption = %v, want none (expiry clears it too)", ws)
	}
}
