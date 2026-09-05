package tdd

import "encoding/json"

// A receipt the gate accepted once carried mutants_total=34, caught=28,
// survivors=4, accepted=41 — 41 accepted out of 34 measured, an arithmetic
// impossibility nothing checked (issue #339). The merge that day was decided
// on Unaccepted, which is the actual rule, so the impossible number changed
// no decision — it just sat there being wrong while a session spent an hour
// deriving from source what the receipt itself could have said. The four
// checks below are exactly the invariants a well-formed receipt already
// satisfies, computed from fields it already carries:
//
//	accepted <= mutants_total
//	caught + len(survivors) + timeout + unviable <= mutants_total
//	len(unaccepted) <= len(survivors)
//	every entry in unaccepted appears in survivors
//
// A receipt failing one is not a receipt the gate can reason about, and is
// refused through blockReceipt — the same rejection family and remedy every
// other receipt refusal uses.

// receiptCoherenceFields are the JSON keys the checks below read. Decoding
// into MutationReceipt can never tell "the producer wrote zero" from "the
// producer has never heard of this field" — both leave Go's int zero value —
// and docs/mutation-runner.md is explicit that "the gate never rejects a
// receipt for a field a runner has not caught up with yet". So presence is
// read off the WIRE bytes directly, the same way this file's neighbours
// already treat an empty BaseSHA or RepoID as "older producer, not a
// mismatch": an invariant is judged only when every field it depends on was
// actually present on the wire; a field that was never written is neither
// confirmed nor contradicted, and the check that needs it is skipped rather
// than fed a zero it never measured.
var receiptCoherenceFields = []string{
	"mutants_total", "caught", "timeout", "unviable", "accepted", "survivors", "unaccepted",
}

// receiptFieldPresence reports which of receiptCoherenceFields the raw
// receipt payload actually wrote. nil for a payload that is not even an
// object, in which case every field reads absent and every check below is
// skipped — json.Unmarshal into MutationReceipt has already refused an
// unparseable receipt by the time this runs.
func receiptFieldPresence(data []byte) map[string]bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	present := make(map[string]bool, len(receiptCoherenceFields))
	for _, f := range receiptCoherenceFields {
		_, present[f] = raw[f]
	}
	return present
}

// checkReceiptUnacceptedCoherence refuses a receipt whose unaccepted list
// cannot itself be true, before the ordinary "an unaccepted survivor blocks
// the merge" rule gets to name one: more unaccepted entries than survivor
// entries, or an unaccepted entry that names no survivor the run measured at
// all. Run first so a self-contradictory receipt is diagnosed as
// self-contradictory rather than read as an ordinary uncaught mutant.
func checkReceiptUnacceptedCoherence(root string, present map[string]bool, r MutationReceipt) *GateResult {
	if !present["unaccepted"] || !present["survivors"] {
		return nil
	}
	if len(r.Unaccepted) > len(r.Survivors) {
		return blockReceipt(root,
			"unaccepted (%d) exceeds survivors (%d) — an unaccepted mutant is one of the run's own survivors, so there can never be more of the former",
			len(r.Unaccepted), len(r.Survivors))
	}
	survived := make(map[mutantKey]bool, len(r.Survivors))
	for _, s := range r.Survivors {
		survived[s.key()] = true
	}
	for _, u := range r.Unaccepted {
		if !survived[u.key()] {
			return blockReceipt(root, "unaccepted entry %s names no survivor the run measured", u.String())
		}
	}
	return nil
}

// checkReceiptCountCoherence refuses a receipt whose category counts cannot
// be true against its own mutants_total: more accepted than measured, or
// caught+survivors+timeout+unviable overcounting the total. Run after the
// verdict, worktree, unaccepted-survivor and timeout checks, which each name
// their own reason first when they apply; this is what catches the shape
// that would otherwise merge silently, as in the real case above.
func checkReceiptCountCoherence(root string, present map[string]bool, r MutationReceipt) *GateResult {
	if present["accepted"] && present["mutants_total"] && r.Accepted > r.MutantsTotal {
		return blockReceipt(root, "accepted (%d) exceeds mutants_total (%d) — a receipt cannot accept more mutants than it measured",
			r.Accepted, r.MutantsTotal)
	}
	if present["caught"] && present["survivors"] && present["timeout"] && present["unviable"] && present["mutants_total"] {
		measured := r.Caught + len(r.Survivors) + r.Timeout + r.Unviable
		if measured > r.MutantsTotal {
			return blockReceipt(root, "caught+survivors+timeout+unviable (%d) exceeds mutants_total (%d) — the categories overcount what the run measured",
				measured, r.MutantsTotal)
		}
	}
	return nil
}
