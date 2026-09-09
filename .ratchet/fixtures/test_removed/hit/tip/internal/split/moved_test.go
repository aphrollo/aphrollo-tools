package split

// all_test.go is gone at tip, so this tombstone clears the first condition —
// but TestMovedOut turns up right here, which says the file was SPLIT, not
// retired. The admission is refused and TestLostInTheSplit, the test that did
// not make the move, is reported under its base path.
// ratchet: test_removed internal/split/all_test.go: subject deleted with the tests

import "testing"

func TestMovedOut(t *testing.T) {
	if 4+4 != 8 {
		t.Fatal("arithmetic broke")
	}
}
