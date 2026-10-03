package render

import "slices"

// Delivery is the record an engine appends once it has written a hook's
// output: which line (by ID), through which hook, to which actor. It is data;
// nothing here stores it.
type Delivery struct {
	ID    string
	Hook  Hook
	Actor string // "session_id/agent_id" of the hook payload the output went to
}

// Deliver is the Delivery of this line through h to actor.
func (l Line) Deliver(h Hook, actor string) Delivery {
	return Delivery{ID: l.ID, Hook: h, Actor: actor}
}

// Reaches says whether the output of a hook is known to be read by the agent.
// Only what F3 recorded counts (§12 F3 results, payloads under
// internal/tdd/internal/tddtest/testdata/hooks): additionalContext at
// PostToolBatch ("after every batch, in -p and interactive") and at
// SubagentStart (verbatim, to the subagent only). PreToolUse, PostToolUse,
// UserPromptSubmit, SessionStart, Stop and SubagentStop fire in the
// recordings, but no recording shows what the agent read of their output, so a
// line delivered through them is not yet seen: it is said again at the next
// proven hook, which costs a line and loses none. A hook is added here with the
// recording that proves it. Setup, CwdChanged and DirectoryAdded did not fire,
// SessionEnd has nobody left to read it, and a hook this build does not know
// is not trusted.
func Reaches(h Hook) bool {
	return h == HookPostToolBatch || h == HookSubagentStart
}

// Due says whether a line has to be said to actor: it says something, and no
// delivery of it through a hook that reaches the agent went to this actor. A
// delivery to another actor does not count (a subagent's context is not its
// parent's), nor does one through a hook nobody reads (§11 F22-F23: seen only
// after a delivery recorded as reaching the agent).
func Due(l Line, actor string, log []Delivery) bool {
	if l.Text == "" {
		return false
	}
	return !slices.ContainsFunc(log, func(d Delivery) bool {
		return d.ID == l.ID && d.Actor == actor && Reaches(d.Hook)
	})
}
