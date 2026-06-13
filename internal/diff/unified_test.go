package diff

import (
	"strings"
	"testing"
)

func TestUnified_SingleLineChange(t *testing.T) {
	before := "alpha\nbeta\ngamma\n"
	after := "alpha\nBETA\ngamma\n"

	got := Unified("x.txt", before, after)

	want := "--- a/x.txt\n" +
		"+++ b/x.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		" alpha\n" +
		"-beta\n" +
		"+BETA\n" +
		" gamma\n"
	if got != want {
		t.Fatalf("Unified mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Distant changes must split into separate hunks; unchanged lines beyond the
// context window are not emitted.
func TestUnified_DistantChangesSplitIntoHunks(t *testing.T) {
	var bb, ab strings.Builder
	for i := 1; i <= 10; i++ {
		line := "line" + string(rune('0'+i%10)) + "\n"
		if i == 1 || i == 10 {
			bb.WriteString("OLD-" + line)
			ab.WriteString("NEW-" + line)
		} else {
			bb.WriteString(line)
			ab.WriteString(line)
		}
	}

	got := Unified("x.txt", bb.String(), ab.String())

	if n := strings.Count(got, "@@ "); n != 2 {
		t.Fatalf("hunk headers = %d, want 2\n%s", n, got)
	}
	if !strings.Contains(got, "-OLD-line1\n+NEW-line1") {
		t.Fatalf("first change missing:\n%s", got)
	}
	if !strings.Contains(got, "-OLD-line0\n+NEW-line0") { // line10 -> i%10==0
		t.Fatalf("last change missing:\n%s", got)
	}
	if strings.Contains(got, " line5") {
		t.Fatalf("unchanged middle line5 should be outside both hunks:\n%s", got)
	}
}

func TestUnified_NoChangeIsEmpty(t *testing.T) {
	src := "alpha\nbeta\n"
	if got := Unified("x.txt", src, src); got != "" {
		t.Fatalf("Unified of identical text = %q, want empty", got)
	}
}
