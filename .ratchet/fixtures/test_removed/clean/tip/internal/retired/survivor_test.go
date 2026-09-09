package retired

// receipt_test.go is gone at tip and nothing it captured — TestReceiptRendered,
// TestReceiptParsed — stands anywhere else, so ONE line naming the PATH and the
// reason admits the whole file. That is the #574 case: a test deleted together
// with the subject it exercised owes a reader the reason, not a roll call.
// ratchet: test_removed internal/retired/receipt_test.go: the receipt and its renderer were deleted; these tests had no other subject

import "testing"

func TestSurvivor(t *testing.T) {
	if 8+8 != 16 {
		t.Fatal("arithmetic broke")
	}
}
