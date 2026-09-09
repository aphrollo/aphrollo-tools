package standing

// A tombstone naming a PATH admits every symbol that stood in that path at
// base — but only once the path is GONE. This file is still here, so the
// line below admits nothing and TestQuietlyDropped is still reported.
// ratchet: test_removed internal/standing/keep_test.go: retired the dropped case with its subject

import "testing"

func TestKept(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("arithmetic broke")
	}
}
