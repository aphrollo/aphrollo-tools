package tdd

// premergeDisplayName is what a human reads: the "gate <name>:" prefix on
// every line the merge gate prints (stderr and GateResult.Message alike).
// premergeLogToken is what gate.log, the mutation-receipt hints keyed on
// stage, and the merge-gate escape fingerprint all index on — kept at the
// hook's pre-rename spelling ("premergecommit") so an already-written
// gate.log line, receipt hint or escape fingerprint never splits across two
// tokens for the SAME stage. The two names diverge only for this one gate:
// appendGateLog is where they reconcile (see its remap), so every stage
// function can just print premergeDisplayName without knowing the log ever
// used a different word for it.
const (
	premergeDisplayName = "premerge"
	premergeLogToken    = "premergecommit"
)
