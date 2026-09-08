package tdd

// The environment the cargo shim reads to tell a mutation run apart from an
// ordinary build. It is all that is left of a much longer list: the rest
// existed to hand a repo-owned producer script the scoping, the diff and the
// flags this side had computed, and the runner is in this binary now, so it
// passes them to the tool directly instead.

const (
	// MutationGateEnv marks a run started by `aphrollo gate mutants run`. The
	// runner holds the box-wide mutation lock around the whole call, so its
	// children may invoke `cargo mutants` (which the shim otherwise refuses)
	// and may build without queueing behind the editors on the box. Nothing
	// else sets it: an ordinary build carrying it is a caller lying about
	// what it is, and the harm is bounded to that build's own target dir.
	MutationGateEnv = "APHROLLO_MUTATION_GATE"
	// MutationGateMarked is the one value that means "yes".
	MutationGateMarked = "1"
)
