package postedit

import (
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
	if err := s.Save(path); err != nil {
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
	session := SessionID()
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
	_ = s.Save(path)
	until, err := time.Parse(time.RFC3339, entry.Until)
	if err != nil {
		return false
	}
	return !discardNow().After(until)
}

// discardBashSpentTTL bounds how long a Bash-hook-approved discard command
// waits for its OWN subprocess to reach the git queue shim: comfortably
// longer than any real gap between a PreToolUse decision and the shell
// actually running the command it just approved, short enough that a spent
// record left over by a command that never actually ran cannot outlive it by
// more than a beat.
const discardBashSpentTTL = 30 * time.Second

// markDiscardBashSpent records that THIS session's Bash/PowerShell discard
// wall (DiscardBashDecision) already spent WallDiscard's one-shot arm
// approving argv, so ConsumeDiscardBashSpent below -- the git queue shim's
// side of the same command -- can still let this EXACT invocation through a
// moment later, instead of finding the session-wide arm already consumed
// (ConsumeOneShot spends it on the FIRST check, regardless of which side
// made it) and refusing a command its own session already approved
// (#857 follow-up: one arm must cover one command end to end). Silent on
// failure: the Bash tool call this covers has already been approved either
// way, so there is nothing here for a caller to act on.
func markDiscardBashSpent(argv []string) {
	session := SessionID()
	if session == "" || len(argv) == 0 {
		return
	}
	s, path := loadSession(session)
	if s == nil {
		return
	}
	now := discardNow()
	entry := DiscardBashSpentEntry{
		Argv:  append([]string{}, argv...),
		Until: now.Add(discardBashSpentTTL).UTC().Format(time.RFC3339),
	}
	s.Overrides.DiscardBashSpent = append(pruneDiscardBashSpent(s.Overrides.DiscardBashSpent, now), entry)
	_ = s.Save(path)
}

// ConsumeDiscardBashSpent reports whether THIS session's Bash/PowerShell
// discard wall has an unexpired spent record for EXACTLY argv -- the git
// queue shim's own half of the split #857 follow-up describes. Removes the
// matching record (and every expired one) either way, so a second, DIFFERENT
// discard command the same session runs next never rides the same window.
func ConsumeDiscardBashSpent(argv []string) bool {
	session := SessionID()
	if session == "" || len(argv) == 0 {
		return false
	}
	s, path := loadSession(session)
	if s == nil {
		return false
	}
	pruned := pruneDiscardBashSpent(s.Overrides.DiscardBashSpent, discardNow())
	found := false
	kept := pruned[:0]
	for _, e := range pruned {
		if !found && argvEqual(e.Argv, argv) {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	s.Overrides.DiscardBashSpent = kept
	_ = s.Save(path)
	return found
}

// pruneDiscardBashSpent drops every entry whose TTL has already passed, so a
// command that never actually reached the shim cannot accumulate forever.
func pruneDiscardBashSpent(entries []DiscardBashSpentEntry, now time.Time) []DiscardBashSpentEntry {
	kept := entries[:0]
	for _, e := range entries {
		until, err := time.Parse(time.RFC3339, e.Until)
		if err == nil && now.After(until) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// argvEqual compares two argv slices element by element — the exact match
// #857's follow-up needs: a spent record for `stash drop stash@{0}` must
// never authorize a DIFFERENT discard command the same session happens to
// run inside the same short window.
func argvEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
