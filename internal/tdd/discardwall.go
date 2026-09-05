package tdd

import (
	"os"
	"sync/atomic"
	"time"
)

// WallDiscard is the discard wall (#343): a git verb that would throw away
// uncommitted or unmerged work, refused unless what it would destroy is
// zero. Unlike WallPrimary's session-long waiver, this one is spent by the
// first command that checks it — see armDiscardWaiver and ConsumeOneShot.
const WallDiscard = "discard"

// discardOneShotTTL bounds how long an armed discard waiver survives even if
// no command ever consumes it: a session that arms it and then never runs
// the command it meant to must not leave the wall open indefinitely.
const discardOneShotTTL = 5 * time.Minute

// discardClockOverride lets a test observe the 5-minute expiry without a
// real sleep. Exported through SetDiscardClockForTest because internal/cli
// drives the discard wall through the git shim, in a different package.
var discardClockOverride atomic.Pointer[func() time.Time]

func discardNow() time.Time {
	if p := discardClockOverride.Load(); p != nil {
		return (*p)()
	}
	return time.Now()
}

// SetDiscardClockForTest overrides the clock the discard wall's one-shot arm
// reads, for the duration of a test.
func SetDiscardClockForTest(clock func() time.Time) (restore func()) {
	prev := discardClockOverride.Swap(&clock)
	return func() { discardClockOverride.Store(prev) }
}

// armDiscardWaiver arms WallDiscard for session, replacing any earlier arm:
// `{armed: true, until: now+5m}`. Returns the deadline so the caller can
// render it into the message it prints.
func armDiscardWaiver(session string) (time.Time, error) {
	s, path := loadSession(session)
	if s == nil {
		return time.Time{}, errNoSession
	}
	if s.Overrides.Waivers == nil {
		s.Overrides.Waivers = map[string]waiverEntry{}
	}
	now := discardNow()
	until := now.Add(discardOneShotTTL)
	s.Overrides.Waivers[WallDiscard] = waiverEntry{
		Since: now.UTC().Format(time.RFC3339),
		Armed: true,
		Until: until.UTC().Format(time.RFC3339),
	}
	if err := s.save(path); err != nil {
		return time.Time{}, err
	}
	return until, nil
}

// ConsumeOneShot reports whether wall has an active one-shot arm for the
// session the environment names, CLEARING it either way: the arm is spent by
// the first command that checks it, expired or not, so an arm nobody used
// before its command ran never lingers for some unrelated later command to
// trip over. False when there is no session in the environment, no armed
// waiver for wall, or the arm's deadline has already passed.
func ConsumeOneShot(wall string) bool {
	session := os.Getenv("CLAUDE_SESSION_ID")
	if session == "" {
		return false
	}
	s, path := loadSession(session)
	if s == nil {
		return false
	}
	entry, ok := s.Overrides.Waivers[wall]
	if !ok || !entry.Armed {
		return false
	}
	delete(s.Overrides.Waivers, wall)
	_ = s.save(path)
	until, err := time.Parse(time.RFC3339, entry.Until)
	if err != nil {
		return false
	}
	return !discardNow().After(until)
}
