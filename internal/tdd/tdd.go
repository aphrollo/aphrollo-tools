// Package tdd implements aphrollo's autonomous TDD gates, ported from the
// retired claude-code-tdd Node hooks into this binary. The gates fire without
// agent cooperation and are tamper-evident by construction; this package keeps
// the gate LOGIC pure and testable, with the Claude-hook and git-hook IO at
// the edges (hook.go) and the CLI surface in internal/cli.
//
// The design principle, carried over and sharpened: edit-time gates are only
// the near-zero-false-positive ones (smells in test files), because a false
// block wedges the agent. Everything heavier — RED/GREEN feedback, fail-first,
// review — runs after the edit, where being wrong costs a re-run, not a stall.
// Throughout, detectors match against a masked copy of the source (see mask.go)
// so a smell mentioned only in a string or comment never trips a gate.
package tdd

// Action is the outcome of a gate evaluating one edit.
type Action int

const (
	// Allow: the edit flows, no output.
	Allow Action = iota
	// Warn: the edit flows but carries advisory context for the model.
	Warn
	// Block: the edit is denied; Reason explains why and how to fix it.
	Block
)

func (a Action) String() string {
	switch a {
	case Warn:
		return "warn"
	case Block:
		return "block"
	default:
		return "allow"
	}
}

// Decision is a gate's verdict for one edit. A blocking Decision always carries
// a Reason that names the smell and the concrete fix — nothing is denied
// silently.
type Decision struct {
	Action Action
	Reason string
	// Policy names the detector that produced this verdict ("test-sleep",
	// "ratchet"), so a denial can be COUNTED by policy rather than read as
	// prose. Empty when nothing tripped.
	Policy string
	// Escapes names every waiver this edit claimed ("smell-escape:<policy>"),
	// so an escape hatch is counted rather than assumed rare.
	Escapes []string
}
