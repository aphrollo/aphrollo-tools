package tdd

import (
	"fmt"
	"time"
)

// The two fail-first stand-downs that refuse a commit (#561). A stage whose
// whole purpose is to prove a staged test goes RED at HEAD must not turn into
// a pass when the box is busy: on 2026-09-07, four lanes sharing two build
// slots produced three commits that landed with nothing proven, each logged
// as "inconclusive (fail-open)" and none of them read at the time.
//
// Both refusals route through verdictFor rather than hand-rolling a
// GateResult: a killed run is exactly its outcomeTimeout shape (elapsed time
// named, box load sampled, "timeout-rejected" in gate.log), and a proof that
// never got a slot is its outcomeCheckError shape — the stage's own machinery
// could not answer, which is the same "nothing was proven" as a timeout from
// a different cause. That also keeps them OUT of `gate stats`' fail-open
// stand-down count, which is how #561 asked to be measured.

// failFirstStageName is the stage token both refusals report under, in the
// stage line and in gate.log.
const failFirstStageName = "fail-first"

// failFirstOverBudgetRefusal refuses a commit whose proof run was killed at
// the stage budget. The remedy names the proof's own command, because the
// question a session then has is which of its tests is slow.
func failFirstOverBudgetRefusal(root, cmd string, out failFirstOutcome) GateResult {
	return verdictFor("precommit", failFirstStageName, root, cmd, stageOutcome{
		Kind:   outcomeTimeout,
		Result: SuiteResult{Duration: out.dur},
		Message: fmt.Sprintf(
			"BLOCKED: fail-first could not prove your staged tests RED — the proof run was killed after %.0fs having measured nothing.\n"+
				"Refusing rather than landing a commit whose test nobody proved.\n"+
				"Remedy: commit again once the box is quieter (`aphrollo gate status` names what is holding it), or run the proof's own command to see what is slow:\n"+
				"    cd %s\n"+
				"    %s",
			out.dur.Seconds(), root, cmd),
	})
}

// failFirstNoBuildSlotRefusal refuses a commit whose proof never started: it
// queued for the machine-wide build lock until the wait budget expired. The
// remedy is a different one — nothing here says the tests are slow, so the
// answer is to see what holds the box and commit again when it frees.
func failFirstNoBuildSlotRefusal(root, cmd string, out failFirstOutcome) GateResult {
	return verdictFor("precommit", failFirstStageName, root, cmd, stageOutcome{
		Kind: outcomeCheckError,
		Err:  fmt.Errorf("no build slot came free in %s, so the proof never ran", out.waited.Round(time.Second)),
		Message: fmt.Sprintf(
			"BLOCKED: fail-first never got a build slot — it queued %s for the box's build lock and gave up, so your staged tests never ran at HEAD.\n"+
				"Refusing rather than landing a commit whose test nobody proved.\n"+
				"Remedy: `aphrollo gate status` names the run holding the slot; commit again once it finishes — a queued commit is not a rejected one.\n"+
				"The proof it will run: %s (in %s)",
			out.waited.Round(time.Second), cmd, root),
	})
}
