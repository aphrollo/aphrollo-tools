package postedit

import "testing"

func TestKnownWall_ExactNamesOnly(t *testing.T) {
	for _, w := range []string{"primary", "discard", "source-bash"} {
		if !KnownWall(w) {
			t.Errorf("KnownWall(%q) = false, want true", w)
		}
	}
	for _, w := range []string{"", "primary ", " primary", "Primary", "source", "source-bash2", "allow"} {
		if KnownWall(w) {
			t.Errorf("KnownWall(%q) = true, want false", w)
		}
	}
}

func TestWallNames_ListsEveryWallInUsageOrder(t *testing.T) {
	if got, want := WallNames(), "primary|discard|source-bash"; got != want {
		t.Fatalf("WallNames() = %q, want %q", got, want)
	}
}
