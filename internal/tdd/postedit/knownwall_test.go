package postedit

import "testing"

// ratchet: test_removed internal/tdd/postedit/sourcebash_test.go: the source-bash wall and its command parser are gone, a shell source write is an edit the post hook judges (bashedit.go)

func TestKnownWall_ExactNamesOnly(t *testing.T) {
	for _, w := range []string{"primary", "discard"} {
		if !KnownWall(w) {
			t.Errorf("KnownWall(%q) = false, want true", w)
		}
	}
	for _, w := range []string{"", "primary ", " primary", "Primary", "source", "source-bash", "allow"} {
		if KnownWall(w) {
			t.Errorf("KnownWall(%q) = true, want false", w)
		}
	}
}

func TestWallNames_ListsEveryWallInUsageOrder(t *testing.T) {
	if got, want := WallNames(), "primary|discard"; got != want {
		t.Fatalf("WallNames() = %q, want %q", got, want)
	}
}
