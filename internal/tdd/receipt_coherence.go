package tdd

import (
	"encoding/json"
	"fmt"
)

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

// checkReceiptSchema judges the receipt's OWN producer version before any
// other field on it is trusted. A schema OLDER than this binary's degrades
// gracefully by construction — every check in this file already reads
// presence off the wire per field (receiptFieldPresence), which is exactly
// what let every receipt-shape change before this one land without a version
// at all. A schema NEWER than this binary understands is the opposite risk:
// it may carry fields, or field MEANINGS, this binary has never seen, and
// reading it as if nothing changed could pass a receipt a binary that
// actually understood it would refuse. So newer is refused outright, naming
// the fix (upgrade the binary) rather than silently interpreted with unknown
// risk — "reject over substitute" for the one field that says how much of
// the rest of the receipt this binary can even read (issue #505).
func checkReceiptSchema(root string, r MutationReceipt) *GateResult {
	if r.Schema <= ReceiptSchemaVersion {
		return nil
	}
	return blockReceipt(root, "schema-newer",
		"this receipt was written by a newer producer (schema %d) than this binary understands (schema %d) — upgrade aphrollo before judging it",
		r.Schema, ReceiptSchemaVersion)
}

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
		return blockReceipt(root, "incoherent-unaccepted",
			"unaccepted (%d) exceeds survivors (%d) — an unaccepted mutant is one of the run's own survivors, so there can never be more of the former",
			len(r.Unaccepted), len(r.Survivors))
	}
	survived := make(map[mutantKey]bool, len(r.Survivors))
	for _, s := range r.Survivors {
		survived[s.key()] = true
	}
	for _, u := range r.Unaccepted {
		if !survived[u.key()] {
			return blockReceipt(root, "incoherent-unaccepted", "unaccepted entry %s names no survivor the run measured", u.String())
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
		return blockReceipt(root, "incoherent-counts", "accepted (%d) exceeds mutants_total (%d) — a receipt cannot accept more mutants than it measured",
			r.Accepted, r.MutantsTotal)
	}
	if present["caught"] && present["survivors"] && present["timeout"] && present["unviable"] && present["mutants_total"] {
		measured := r.Caught + len(r.Survivors) + r.Timeout + r.Unviable
		if measured > r.MutantsTotal {
			return blockReceipt(root, "incoherent-counts", "caught+survivors+timeout+unviable (%d) exceeds mutants_total (%d) — the categories overcount what the run measured",
				measured, r.MutantsTotal)
		}
	}
	return nil
}

// vacuousMutationRun reports whether a run's own counts describe a run that
// measured nothing: no mutants generated, and no diff lines moved to explain
// why. MovedLines > 0 stays exempt — the plan decided there was nothing left
// to measure because git's own move detection accounted for every line the
// diff changed, a real, explained answer rather than a scope that matched
// nothing.
//
// judgeGoMutantsCI (mutants_ci.go) and checkReceiptNotVacuous below share
// this one predicate, so the CI judge and the merge gate cannot again
// disagree about the same receipt the way issue #386 found them doing: CI
// called an all-zero receipt not-a-proof while the merge gate, which never
// looked at mutants_total at all, called it a pass and let it in.
func vacuousMutationRun(mutantsTotal, movedLines int) bool {
	return mutantsTotal == 0 && movedLines == 0
}

// checkReceiptNotVacuous refuses a receipt that is not self-contradictory
// (checkReceiptCountCoherence's family) but simply empty: mutants_total and
// moved_lines both zero, on a lane laneHasNothingToMutate has already ruled
// out as having no source or test file to mutate BY PATH. A run that measured
// nothing over a diff that plainly has something to mutate is
// indistinguishable, from the zero counts alone, from a run that genuinely
// had nothing to measure — a wrong or stale --diff base and a diff whose only
// changed source is a deletion or a comment-only edit produce the identical
// pair of zeros (issue #386, sharpened by issue #494's two further cases).
//
// ZeroReason breaks that tie by reading a fact the producer already has
// rather than inferring one from the two counts: a receipt that names its own
// reason is judged by it, never re-guessed at from mutants_total and
// moved_lines a second time (the whole point of #494 — PATH-based
// classification here must not grow a clause for every new way cargo-mutants
// can find nothing).
//
// Judged only when the producer actually wrote mutants_total: an older
// producer that never emitted the field is neither confirmed nor
// contradicted, same doctrine as every other check in this file.
func checkReceiptNotVacuous(root string, present map[string]bool, r MutationReceipt) *GateResult {
	if !present["mutants_total"] || !vacuousMutationRun(r.MutantsTotal, r.MovedLines) {
		return nil
	}
	if r.ZeroReason != "" {
		// The producer already said why: a scope that resolved and
		// genuinely holds no mutable content is a different fact from a
		// scope that resolved to nothing, and only the producer can tell
		// them apart. It just did.
		return nil
	}
	// The producer's OWN version, when it is present and behind this
	// binary's, resolves the exact ambiguity the fallback message below has
	// to hedge about: an older producer that never learned to explain a zero
	// is a known cause, not a maybe, so name it instead of sending the
	// session to re-check a base that was never wrong. r.Schema == 0 is
	// EXCLUDED here on purpose — it is every receipt written before this
	// field existed, still indistinguishable from a current producer that
	// left it unset (issue #505's stated limit: this does not retroactively
	// help those; it starts paying off at the field ReceiptSchemaVersion
	// grows to 2 for).
	if r.Schema != 0 && r.Schema < ReceiptSchemaVersion {
		return blockReceipt(root, "vacuous-older-producer", "%s", olderProducerZeroReasonMessage(r.Schema, ReceiptSchemaVersion))
	}
	return blockReceipt(root, "vacuous", "mutants_total is 0 and moved_lines is 0 — a scope that matches nothing is not a proof: "+
		"either the base is wrong (check it is the merge base this branch actually diverged from), or the diff genuinely holds no mutable source and this producer has not caught up to say so (docs/mutation-runner.md)")
}

// olderProducerZeroReasonMessage is the message checkReceiptNotVacuous names
// once a receipt's own schema says it predates the field it needs. Split out
// as a pure function of both schema numbers, rather than reading
// ReceiptSchemaVersion directly, so the exact wording is pinned at the unit
// level independent of the current version constant: today ReceiptSchemaVersion
// is 1 and no receipt can carry a present-but-lower schema (the only value
// below 1 is 0, which reads as absent, not older — see checkReceiptNotVacuous),
// so this branch is not yet reachable through a real receipt. It starts firing
// the day ReceiptSchemaVersion moves to 2, and is tested now so the wording is
// right before that day, not after.
func olderProducerZeroReasonMessage(producerSchema, wantSchema int) string {
	return fmt.Sprintf("this receipt was written by an older producer (schema %d, this binary writes %d) — re-run `aphrollo gate mutants run` rather than checking the base",
		producerSchema, wantSchema)
}
