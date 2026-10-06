package postedit

import (
	"strings"
	"testing"
)

// red-green is a wall `gate allow` can waive: the deny of the red→green rule names
// that as its override, so the verb has to take it.
func TestWalls_RedGreenIsWaivableAndNamedInTheUsage(t *testing.T) {
	if !KnownWall(WallRedGreen) || WallRedGreen != "red-green" {
		t.Errorf("KnownWall(%q) = false, want the red-green wall to be waivable", WallRedGreen)
	}
	if !strings.Contains(WallNames(), "red-green") {
		t.Errorf("WallNames() = %q, want it to name red-green", WallNames())
	}
}

func TestRedGreenWaived_IsTheSessionsWaiverOnTheWall(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	if RedGreenWaived("s-waive") {
		t.Fatal("a session that waived nothing has the red-green wall waived")
	}
	if _, err := AllowWallForSession("s-waive", t.TempDir(), WallRedGreen); err != nil {
		t.Fatalf("AllowWallForSession: %v", err)
	}
	if !RedGreenWaived("s-waive") || RedGreenWaived("another-session") {
		t.Errorf("RedGreenWaived: s-waive = %v, another-session = %v, want true and false", RedGreenWaived("s-waive"), RedGreenWaived("another-session"))
	}
}
