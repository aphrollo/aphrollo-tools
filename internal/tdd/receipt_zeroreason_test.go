package tdd

import (
	"strings"
	"testing"
)

// The producer already knows why a receipt reports zero mutants: cargo-mutants
// told it, in its own words, that the diff carries no mutable source (a
// comment-only edit, e.g. lane/testrig-edge, issue #494). A receipt that
// carries that reason must merge — refusing it sends every comment-only or
// deletion-only fix chasing a base that was never wrong.
func TestMutationReceipt_MergesAZeroMutantReceiptThatNamesItsOwnZeroReason(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.MutantsTotal, r.Caught = 0, 0
	r.ZeroReason = ReceiptZeroReasonNoMutableSource
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip, RepoRoot: repoWithMutationScript(t)})
	if got != nil {
		t.Fatalf("a zero-mutant receipt that names its own reason must merge: %s", got.Message)
	}
}

// The exact case the vacuous check exists for — a receipt built against the
// wrong base, whose scope matched nothing and says nothing about why — must
// still be refused. A wrong scope and an empty-but-correct scope produce the
// identical pair of zeros; only ZeroReason tells them apart, and a receipt
// that never set it earns no benefit of the doubt. If this stops being
// refused, the fix has removed a real guard rather than fixed a false one.
func TestMutationReceipt_StillRefusesAZeroMutantReceiptWithNoZeroReason(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.MutantsTotal, r.Caught = 0, 0
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip, RepoRoot: repoWithMutationScript(t)})
	if got == nil || !got.Blocked {
		t.Fatal("an unexplained zero-mutant receipt must still be refused — this is the wrong-base case the vacuous check exists to catch")
	}
	if !strings.Contains(got.Message, "mutants_total is 0 and moved_lines is 0") {
		t.Fatalf("message = %q, want the vacuous rejection", got.Message)
	}
}
