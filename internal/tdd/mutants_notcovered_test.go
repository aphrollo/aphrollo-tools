package tdd

import "testing"

// gremlins says NOT COVERED when its coverage profile has no block at the
// mutant's position — which is NOT the same claim as "a test ran and did not
// notice". It never ran a test at all.
//
// Its mapping is demonstrably unreliable. In this repo, for the line
//
//	return strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
//
// gremlins classifies the two mutants INSIDE the closure (col 59, col 70) as
// RUNNABLE and the outer `< 0` at col 79 as NOT COVERED, because Go's coverage
// blocks split at the closure and nothing covers the position after its body.
// That outer comparison is plainly executed, and a hand-mutation of it fails
// TestIsAllDigits_LeavesANonDigitFirstCharacterSuffixUntrimmed. On Windows the
// same mapping fails wholesale: 4890 mutants NOT COVERED, mutator coverage
// 0.00%.
//
// Counting an unmeasured mutant as a survivor turns that into a merge refusal
// naming code a test already constrains. It is reported and counted, never
// silently dropped — but it is not a survivor.

// TestGremlinsStatus_NotCoveredIsItsOwnStatusNotAMiss pins the mapping.
func TestGremlinsStatus_NotCoveredIsItsOwnStatusNotAMiss(t *testing.T) {
	t.Parallel()
	if got := gremlinsStatus("NOT COVERED"); got != gremlinsNotCovered {
		t.Errorf("gremlinsStatus(%q) = %q, want %q — an unmeasured mutant is not a miss", "NOT COVERED", got, gremlinsNotCovered)
	}
	// LIVED is the real thing and must keep counting as a miss: a test ran and
	// did not notice.
	if got := gremlinsStatus("LIVED"); got != "missed" {
		t.Errorf("gremlinsStatus(%q) = %q, want %q", "LIVED", got, "missed")
	}
}

// The behaviour the merge reads: a NOT COVERED mutant must not appear among
// the unaccepted survivors, because a non-empty unaccepted list is what
// refuses a merge.
// ratchet: test_removed TestGoMutantsReceipt_DoesNotCountAnUnmeasuredMutantAsASurvivor: renamed and re-pointed at judgeMutants, which is what classifies an outcome now; the claim is unchanged
func TestJudgeMutants_DoesNotCountAnUnmeasuredMutantAsASurvivor(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{}, []MutantOutcome{
		{File: "a.go", Line: 1, Mutation: "x", Status: "caught"},
		{File: "a.go", Line: 2, Mutation: "y", Status: gremlinsNotCovered},
	})

	if v.Refused {
		t.Errorf("verdict refused on an unmeasured mutant:\n%s", v.Message)
	}
	if len(v.Unaccepted) != 0 {
		t.Errorf("Unaccepted = %v, want empty — an unmeasured mutant must not refuse a merge", v.Unaccepted)
	}
	if v.NotCovered != 1 {
		t.Errorf("NotCovered = %d, want 1 — the unmeasured mutant must still be counted", v.NotCovered)
	}
	if v.Unviable != 0 {
		t.Errorf("Unviable = %d, want 0 — not-covered is counted on its own, never folded into unviable", v.Unviable)
	}
	if v.Caught != 1 {
		t.Errorf("Caught = %d, want 1", v.Caught)
	}
}

// The other direction: this must not make a real survivor disappear.
// ratchet: test_removed TestGoMutantsReceipt_StillCountsALivedMutantAsASurvivor: renamed and re-pointed at judgeMutants; the claim is unchanged
func TestJudgeMutants_StillCountsALivedMutantAsASurvivor(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{}, []MutantOutcome{
		{File: "a.go", Line: 3, Col: 1, Mutation: "z", Status: "missed"},
	})

	if len(v.Unaccepted) != 1 || !v.Refused {
		t.Errorf("verdict = %+v, want the one LIVED mutant to refuse the merge", v)
	}
}
