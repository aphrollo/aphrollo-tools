package tdd

import "testing"

// NoteMergeGateEscape excludes the receipt family from the escape loop on
// purpose: a lane with no receipt is refused by a stage the pre-commit gate
// does not run at all, so it is the gate WORKING, and it is the most common
// merge rejection there is. Its own comment says recording it "would make the
// loop's loudest signal its noisiest".
//
// It did exactly that. isReceiptRejection keys on a sentence that only
// blockReceipt writes, and the missing-receipt rejection is deliberately not
// built by blockReceipt — it is one line ending in the remedy, with no room
// for that sentence. So the message
//
//	gate: mutation receipt missing for tree a1d6c360b0b5 — run aphrollo gate mutants origin/main
//
// fell through to the generic branch and was filed as an escape against the
// pre-commit gate, which never had a receipt stage to miss.
func TestIsReceiptRejection_RecognisesTheMissingReceiptRefusalToo(t *testing.T) {
	msg := blockMissingReceipt(receiptContext{TipTree: "a1d6c360b0b5c0de1234"}).Message

	if !isReceiptRejection(msg) {
		t.Errorf("isReceiptRejection(%q) = false — the most common merge refusal there is gets filed as an escape against a gate that has no receipt stage", msg)
	}
}

// ...and the family stays narrow: a stage rejection is still an escape
// candidate, which is the whole point of the loop.
func TestIsReceiptRejection_IsStillFalseForAStageRejection(t *testing.T) {
	for _, msg := range []string{
		"gate premergecommit: clippy failed on the combined tree",
		"gate premergecommit: gofmt → REJECTED\n  internal/workspace/pr_test.go",
	} {
		if isReceiptRejection(msg) {
			t.Errorf("isReceiptRejection(%q) = true — a stage rejection is evidence about the gate and must stay recordable", msg)
		}
	}
}
