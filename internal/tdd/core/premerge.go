package core

// premergeDisplayName is what a human reads: the "gate <name>:" prefix on
// every line the merge gate prints (stderr and GateResult.Message alike).
// premergeLogToken is what gate.log and the merge-gate escape fingerprint
// both index on — kept at the hook's pre-rename spelling ("premergecommit")
// so an already-written gate.log line or escape fingerprint never splits
// across two tokens for the SAME stage. The two names diverge only for this one gate:
// appendGateLog is where they reconcile (see its remap), so every stage
// function can just print premergeDisplayName without knowing the log ever
// used a different word for it.
const (
	premergeDisplayName = "premerge"
	premergeLogToken    = "premergecommit"
)
