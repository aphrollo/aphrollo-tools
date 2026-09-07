package tdd

// The environment names the cargo shim reads to tell a mutation run apart
// from an ordinary build. They are all that is left of a much longer list: the
// rest existed to hand a repo-owned producer script the scoping, the diff and
// the flags this side had computed, and the runner is in this binary now, so
// it passes them to the tool directly instead.

const (
	// MutationGateEnv marks a mutation run started through the gate. It is
	// the retired handshake: the shim now recognises a mutation build by the
	// target dir it writes into, and logs a call that still relies on this.
	MutationGateEnv = "MUTATION_GATE"
	// QueueEnv=bypass lets a run past the build queue, honoured ONLY when the
	// target dir is the mutants worktree's own (see the cargo shim).
	QueueEnv = "APHROLLO_QUEUE"
	// QueueBypass is the one value that means "do not queue".
	QueueBypass = "bypass"
)
