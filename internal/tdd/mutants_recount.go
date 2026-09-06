package tdd

import "encoding/json"

// recountReceipt derives every count and every survivor list from the merged
// outcome set. Counting only the CAUGHT carried ones was the hole: a survivor
// measured on an earlier commit was added to the outcomes and to the total but
// left out of Survivors and Unaccepted, so a commit touching only a.rs merged
// with a known unkilled mutant in b.rs.
//
// The producer's accept-list is honoured rather than re-derived: a survivor
// the receipt listed WITHOUT listing it as unaccepted is one the repo accepted
// with a reason, and that decision is the producer's to make.
//
// outcomesFieldPresent says whether the receipt this r came from actually
// carried an "outcomes" key on the wire — a fact the caller must read off the
// raw bytes (see receiptHasOutcomesField), because a nil Outcomes slice reads
// identically whether the key was written empty or never written at all. When
// Outcomes is empty AND the key was never present, there is nothing to
// recount FROM: a shell producer (tools/mutation_gate.sh, borld's own) writes
// mutants_total, caught, timeout, unviable, survivors and unaccepted directly
// and never an outcomes array, so those fields ARE the run's only
// measurement. Rebuilding from zero outcomes in that shape used to zero every
// one of them and null out Survivors and Unaccepted, and the merge gate —
// since it started reading mutants_total — then refused every such run as
// vacuous (issue #445). A producer that DOES emit Outcomes and genuinely
// measured none this run (the key present, an empty array) still recounts to
// zero as before: that shape is a real, explained answer, not lost evidence.
func recountReceipt(r *MutationReceipt, outcomesFieldPresent bool) {
	if len(r.Outcomes) == 0 && !outcomesFieldPresent {
		return
	}
	accepted := map[mutantKey]bool{}
	unaccepted := map[mutantKey]bool{}
	for _, m := range r.Unaccepted {
		unaccepted[m.key()] = true
	}
	for _, m := range r.Survivors {
		if !unaccepted[m.key()] {
			accepted[m.key()] = true
		}
	}

	sortOutcomes(r.Outcomes)
	r.MutantsTotal, r.Caught, r.Timeout, r.Unviable, r.Accepted = 0, 0, 0, 0, 0
	r.Survivors, r.Unaccepted = nil, nil
	for _, m := range r.Outcomes {
		r.MutantsTotal++
		switch m.Status {
		case "caught":
			r.Caught++
		case "timeout":
			// A timeout the producer accepted is a decision, not an
			// unmeasured mutant: some mutations cannot be measured by any
			// run (an INCREMENT_DECREMENT on a loop index cancels the loop's
			// own increment, so the function never returns). Honour it the
			// same way an accepted survivor is honoured -- by the names the
			// receipt carries -- and count every other timeout as before.
			if accepted[m.key()] {
				r.Survivors = append(r.Survivors, m.name())
				r.Accepted++
				continue
			}
			r.Timeout++
		case "unviable":
			r.Unviable++
		default:
			r.Survivors = append(r.Survivors, m.name())
			if accepted[m.key()] {
				r.Accepted++
				continue
			}
			r.Unaccepted = append(r.Unaccepted, m.name())
		}
	}
}

// receiptHasOutcomesField reports whether the raw receipt bytes carried an
// "outcomes" key at all, distinguishing a producer that supports Outcomes and
// genuinely measured none this run (key present, empty array) from one that
// never emits Outcomes in the first place (key absent) — see recountReceipt.
// false for bytes that are not even a JSON object, which readReceiptFileRaw's
// own json.Unmarshal has already refused to carry this far in every real
// caller.
func receiptHasOutcomesField(data []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw["outcomes"]
	return ok
}
