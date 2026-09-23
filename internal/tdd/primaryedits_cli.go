package tdd

import (
	"errors"
	"sort"
	"time"
)

// Waiver is one active wall waiver, as ListWaivers reports it: which wall,
// since when, and which session holds it. Until is zero for a plain
// session-scoped waiver, and the one-shot arm's deadline for one that isn't.
type Waiver struct {
	Wall    string
	Since   time.Time
	Session string
	Until   time.Time
}

// AllowWall waives wall for the session the environment names, and returns
// the line to print.
//
// The refusal names two overrides and neither was reachable from inside a
// turn. `/tdd primary-edits on` is a UserPromptSubmit hook, so it fires on
// text a person types and an agent cannot invoke it. `APHROLLO_PRIMARY_EDITS=1`
// is read by the hook process, which is a different process from the shell
// that would export it — the variable is gone by the next call, and the
// PreToolUse hook judging an Edit never sees it. So a session that hit the
// rule legitimately had no way to act on what the message told it to do.
//
// The session id is read through SessionID, which knows both names the
// environment may carry: Claude Code sets CLAUDE_CODE_SESSION_ID, and every
// reader here originally asked only for CLAUDE_SESSION_ID, which nothing
// sets. That is why this override answered "no session in the environment"
// to every caller — the session was there under the other name.
func AllowWall(wall string) (string, error) {
	session, err := envSession()
	if err != nil {
		return "", err
	}
	if wall == WallDiscard {
		until, err := armDiscardWaiver(session)
		if err != nil {
			return "", err
		}
		LogOverride("override-"+wall+"-allow", session, "")
		return discardArmedMessage(until), nil
	}
	if err := setWaiver(session, wall, true); err != nil {
		return "", err
	}
	LogOverride("override-"+wall+"-allow", session, "")
	return waiverAllowedMessage(wall), nil
}

// Revoke restores wall for the session the environment names, and returns
// the line to print.
func Revoke(wall string) (string, error) {
	session, err := envSession()
	if err != nil {
		return "", err
	}
	if err := setWaiver(session, wall, false); err != nil {
		return "", err
	}
	LogOverride("override-"+wall+"-revoke", session, "")
	return waiverRevokedMessage(wall), nil
}

// Waived reports whether the environment's session has an active waiver on
// wall.
func Waived(wall string) bool {
	session := SessionID()
	if session == "" {
		return false
	}
	return waivedForSession(session, wall)
}

// ListWaivers returns every active wall waiver recorded in any session's
// state file, sorted by wall then session — so `gate allow` (bare) prints a
// deterministic picture of what is waived on this box right now, regardless
// of which session waived it.
func ListWaivers() []Waiver {
	var out []Waiver
	for _, session := range everySessionID() {
		s, _ := loadSession(session)
		if s == nil {
			continue
		}
		for wall, entry := range s.Overrides.Waivers {
			since, _ := time.Parse(time.RFC3339, entry.Since)
			w := Waiver{Wall: wall, Since: since, Session: session}
			if entry.Armed {
				w.Until, _ = time.Parse(time.RFC3339, entry.Until)
			}
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Wall != out[j].Wall {
			return out[i].Wall < out[j].Wall
		}
		return out[i].Session < out[j].Session
	})
	return out
}

// envSession is the one identifier a command run from inside a turn can rely
// on — see Allow's doc comment.
func envSession() (string, error) {
	session := SessionID()
	if session == "" {
		return "", errors.New("no session in the environment (neither CLAUDE_SESSION_ID nor CLAUDE_CODE_SESSION_ID is set), so there is nothing to override — an edit would still be refused")
	}
	return session, nil
}

// waiverAllowedMessage and waiverRevokedMessage are wall-specific only for
// the wall this release actually has; a later wall picks its own wording the
// same way.
func waiverAllowedMessage(wall string) string {
	if wall == WallPrimary {
		return "Primary-checkout edits ALLOWED for this session — the merge-only rule is waived. Run `aphrollo gate revoke primary` to restore it."
	}
	return wall + " ALLOWED for this session. Run `aphrollo gate revoke " + wall + "` to restore it."
}

// discardArmedMessage is AllowWall(WallDiscard)'s reply: it names the
// deadline rather than "for this session" (waiverAllowedMessage's shape),
// since the arm is spent by the next command it applies to, not by the
// session ending.
func discardArmedMessage(until time.Time) string {
	return "Discard ARMED for one command in this session (until " + until.UTC().Format(time.RFC3339) +
		"). Run `aphrollo gate revoke discard` to disarm."
}

func waiverRevokedMessage(wall string) string {
	if wall == WallPrimary {
		return "Primary-checkout edits refused again for this session."
	}
	return wall + " restored for this session."
}

// SetPrimaryEditsForEnvSession is the pre-rename spelling of AllowWall/Revoke
// applied to WallPrimary, kept as a silent alias for one release.
func SetPrimaryEditsForEnvSession(on bool) (string, error) {
	if on {
		return AllowWall(WallPrimary)
	}
	return Revoke(WallPrimary)
}
