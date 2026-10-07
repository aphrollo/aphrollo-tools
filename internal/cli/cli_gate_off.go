package cli

import (
	"encoding/json"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// whenOff is what a session hook does while its session is switched off
// (`/aphrollo off`, `/tdd off`, or TRELLIS_OFF for the whole process).
type whenOff int

const (
	// offSilent: the hook answers nothing and decides nothing.
	offSilent whenOff = iota
	// offSwitchOnly: the hook goes on, but only the session switch itself
	// answers; every injection is silent (tdd.HandlePrompt). Without it
	// `/aphrollo on` could not be typed.
	offSwitchOnly
	// offWallsOnly: only the walls that block every author run
	// (alwaysOnWalls); the primary wall, the red-to-green guidance and the
	// guardrail's warnings are the session's, and say nothing.
	offWallsOnly
	// offBookkeeping: the hook goes on, because it only tidies the session
	// and says nothing either way.
	offBookkeeping
)

// sessionHooks is every hook event a session fires, with what it does while
// the session is off. It is also the list of hook subcommands runGate accepts:
// a new hook is a new row, and it cannot be added without saying what it does
// when off. The git-side gates (precommit, premerge, prepush, commit-msg and
// the shims) are not here: they belong to the repo, not the session.
var sessionHooks = map[string]whenOff{
	"sessionstart":       offSilent,
	"pretooluse":         offWallsOnly,
	"posttooluse":        offSilent,
	"posttoolusefailure": offSilent,
	"userpromptsubmit":   offSwitchOnly,
	"sessionend":         offBookkeeping,
	"stop":               offSilent,
	"subagentstop":       offSilent,
	"taskcompleted":      offSilent,
}

// alwaysOnWalls are judged on a PreToolUse payload even while the session is
// off: a wall that blocks every author, human included. It is empty: nothing
// in the live hook is such a wall yet, and a wall that is added (the planned
// secrets one, docs/trellis-architecture.md §5, C14) is one entry here.
var alwaysOnWalls []func(raw []byte) tdd.Decision

// offSession reports whether the hook that read raw belongs to a session that
// is switched off, and so does what sessionHooks says for it instead.
func offSession(hook string, raw []byte) (whenOff, bool) {
	mode := sessionHooks[hook]
	if mode == offSwitchOnly || mode == offBookkeeping {
		return mode, false // these go on; they answer nothing of the gate's while off
	}
	var in struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &in)
	return mode, tdd.SessionOff(in.SessionID)
}

// runWhenOff is the answer of a hook of an off session: nothing, or for
// PreToolUse the always-on walls' decision alone.
func runWhenOff(mode whenOff, raw []byte, stdout io.Writer) int {
	if mode != offWallsOnly {
		return 0
	}
	for _, wall := range alwaysOnWalls {
		if d := wall(raw); d.Action == tdd.Block {
			payload, code := tdd.RenderPreToolUse(d)
			if len(payload) > 0 {
				stdout.Write(payload)
			}
			return code
		}
	}
	return 0
}

// bindHookActor names the session, and for a subagent's call the agent, that
// the hook process serves, once, where every session hook starts: the events it
// appends are then attributed to "session/agent" or to the session, whichever
// call site appended them.
func bindHookActor(raw []byte) (unbind func()) {
	var in struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	_ = json.Unmarshal(raw, &in)
	tdd.SetHookActor(in.SessionID, in.AgentID)
	return func() { tdd.SetHookActor("", "") }
}
