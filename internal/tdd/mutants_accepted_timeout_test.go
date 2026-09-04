package tdd

import (
	"path/filepath"
	"testing"
)

// The merge gate judges a timeout BEFORE it judges acceptance, and the
// accept-list was only ever applied to survivors. So a mutant argued
// unkillable and accepted with its reason still landed in the timeout count
// and still blocked the merge -- on every run, however many jobs it was
// given, because the argument is that no run can ever measure it.
//
// The real case: INCREMENT_DECREMENT on a `for i := 0; i < len(args); i++`
// loop's skip over a flag's value turns `i++` into `i--`, which cancels the
// loop's own increment. The function never returns, so there is no value to
// assert and no message to match; the only observable is the absence of
// progress, which the runner reports as TIMED OUT. Accepting it is the only
// available answer, so acceptance has to reach it.
func TestGoMutantsReceipt_AcceptsATimedOutMutantThatIsOnTheAcceptList(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-accept = [\n  \"internal/cli/loop.go:10 INCREMENT_DECREMENT # decrementing the loop index cancels the loop's own increment, so the function never returns and no test can observe a value\",\n]\n")

	r := goMutantsReceipt(goMutantsRun{Worktree: root}, []MutantOutcome{
		{File: filepath.FromSlash("internal/cli/loop.go"), Line: 10, Mutation: "INCREMENT_DECREMENT", Status: "timeout"},
	}, TreeState{})

	if r.Timeout != 0 {
		t.Errorf("Timeout = %d, want 0 — an accepted mutant must not also be counted as an unmeasured one, or the merge is blocked forever", r.Timeout)
	}
	if r.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 — the accept-list entry states its reason, so it applies", r.Accepted)
	}
	if len(r.Unaccepted) != 0 {
		t.Errorf("Unaccepted = %q, want none", r.Unaccepted)
	}
}

// TestGoMutantsReceipt_StillCountsATimedOutMutantNobodyAccepted is the guard
// against over-correcting: a timeout with no accept-list entry is still an
// unmeasured mutant and must still block.
func TestGoMutantsReceipt_StillCountsATimedOutMutantNobodyAccepted(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-accept = []\n")

	r := goMutantsReceipt(goMutantsRun{Worktree: root}, []MutantOutcome{
		{File: filepath.FromSlash("internal/cli/loop.go"), Line: 10, Mutation: "INCREMENT_DECREMENT", Status: "timeout"},
	}, TreeState{})

	if r.Timeout != 1 {
		t.Errorf("Timeout = %d, want 1 — a timeout nobody argued for is an unmeasured mutant, not a result", r.Timeout)
	}
	if r.Accepted != 0 {
		t.Errorf("Accepted = %d, want 0", r.Accepted)
	}
}

// ...and an accept-list entry with no stated reason is not an argument, so it
// must not silence a timeout either. acceptedMutants already drops those; this
// pins that the timeout path reads the same list rather than a laxer one.
func TestGoMutantsReceipt_IgnoresAnAcceptEntryWithNoReasonForATimeout(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-accept = [\n  \"internal/cli/loop.go:10 INCREMENT_DECREMENT\",\n]\n")

	r := goMutantsReceipt(goMutantsRun{Worktree: root}, []MutantOutcome{
		{File: filepath.FromSlash("internal/cli/loop.go"), Line: 10, Mutation: "INCREMENT_DECREMENT", Status: "timeout"},
	}, TreeState{})

	if r.Timeout != 1 {
		t.Errorf("Timeout = %d, want 1 — an accept-list entry that states no reason is not an argument", r.Timeout)
	}
}
