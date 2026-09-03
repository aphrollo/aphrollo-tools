package tdd

import (
	"testing"
)

// carriedReceipt writes a producer's receipt for laneTip with the outcomes it
// measured itself, then folds in what the run carried, and reads the result.
func carriedReceipt(t *testing.T, measured []MutantOutcome, carried []MutantOutcome, r MutationReceipt) MutationReceipt {
	t.Helper()
	r.Repo, r.TipTree, r.Verdict = "borld", laneTip, receiptVerdictPass
	r.Outcomes = measured
	path := MutationReceiptPathFor(laneTip)
	writeReceiptFile(path, r)
	adoptCarriedOutcomes(MutantsJob{Repo: "borld", TipTree: laneTip}, carried)
	got, ok := readReceiptFile(path)
	if !ok {
		t.Fatal("the receipt disappeared")
	}
	return got
}

// The hole: a carried outcome was added to Outcomes and MutantsTotal, and
// counted only when it was CAUGHT. A survivor measured on an earlier commit
// therefore vanished from Survivors and Unaccepted, and a commit touching only
// a.rs merged with a known unkilled mutant in b.rs.
func TestAdoptCarriedOutcomes_ACarriedSurvivorStillBlocksTheMerge(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	survivor := MutantOutcome{File: "crates/editor_server/src/history.rs", Line: 181, Col: 58,
		Mutation: "replace > with >= in LastEditSeq::superseding_client",
		Package:  "crates/editor_server", Blob: "b1", Fence: "f1", Status: "missed"}
	measured := []MutantOutcome{{File: "crates/editor_client/src/creator.rs", Line: 101, Col: 33,
		Mutation: "replace || with && in send_undo_redo", Package: "crates/editor_client",
		Blob: "b2", Fence: "f2", Status: "caught"}}

	r := carriedReceipt(t, measured, []MutantOutcome{survivor},
		MutationReceipt{MutantsTotal: 1, Caught: 1})

	if len(r.Survivors) != 1 || r.Survivors[0].Line != 181 {
		t.Fatalf("Survivors = %+v, want the carried survivor", r.Survivors)
	}
	if len(r.Unaccepted) != 1 {
		t.Fatalf("Unaccepted = %+v, want the carried survivor to block", r.Unaccepted)
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip}); got == nil || !got.Blocked {
		t.Fatal("a carried, unkilled mutant merged")
	}
}

// A survivor the repo accepted WITH a reason stays accepted when it is
// carried: the accept-list is the producer's, and re-deriving Unaccepted must
// not throw it away.
func TestAdoptCarriedOutcomes_KeepsAnAcceptedSurvivorAccepted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	accepted := MutantOutcome{File: "a.rs", Line: 10, Col: 3, Mutation: "replace > with >=",
		Package: "p", Blob: "b1", Fence: "f1", Status: "missed"}

	r := carriedReceipt(t, []MutantOutcome{accepted}, []MutantOutcome{accepted},
		MutationReceipt{MutantsTotal: 1, Accepted: 1,
			Survivors: []MutantName{{File: "a.rs", Line: 10, Col: 3, Mutation: "replace > with >="}}})

	if len(r.Unaccepted) != 0 {
		t.Fatalf("Unaccepted = %+v, want the producer's accept-list respected", r.Unaccepted)
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip}); got != nil && got.Blocked {
		t.Fatalf("an accepted survivor blocked the merge: %s", got.Message)
	}
}

// A carried TIMEOUT is an unmeasured mutant, and the refusal that exists for
// timeouts has to see it.
func TestAdoptCarriedOutcomes_ACarriedTimeoutIsStillATimeout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	timedOut := MutantOutcome{File: "a.rs", Line: 3, Col: 9, Mutation: "replace * with +",
		Package: "p", Blob: "b1", Fence: "f1", Status: "timeout"}

	r := carriedReceipt(t, nil, []MutantOutcome{timedOut}, MutationReceipt{})

	if r.Timeout != 1 {
		t.Fatalf("Timeout = %d, want the carried one counted", r.Timeout)
	}
	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a carried timeout merged")
	}
}

// The counts describe the merged set, not the producer's half of it.
func TestAdoptCarriedOutcomes_CountsDescribeTheMergedSet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	mk := func(line int, status string) MutantOutcome {
		return MutantOutcome{File: "a.rs", Line: line, Col: 1, Mutation: "m", Package: "p",
			Blob: "b1", Fence: "f1", Status: status}
	}
	r := carriedReceipt(t, []MutantOutcome{mk(1, "caught")},
		[]MutantOutcome{mk(2, "caught"), mk(3, "unviable")},
		MutationReceipt{MutantsTotal: 1, Caught: 1})

	if r.MutantsTotal != 3 || r.Caught != 2 || r.Unviable != 1 {
		t.Fatalf("counts = total %d caught %d unviable %d, want 3/2/1", r.MutantsTotal, r.Caught, r.Unviable)
	}
}
