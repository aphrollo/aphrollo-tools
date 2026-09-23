package tdd

// GateResult is the verdict of a git-time gate (precommit, prepush). A
// blocked action always carries a Message explaining what failed and how to
// proceed. verdictFor owns the actual outcome→verdict mapping: a TIMEOUT or
// a check-error (the check's own machinery could not answer) BLOCKS like a
// failure, since a commit the gate never tested must not land. Message can
// also ride with Blocked false — a deliberate, logged stand-down.
type GateResult struct {
	Blocked bool
	Message string
}
