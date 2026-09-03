package diff

import (
	"fmt"
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

// A single changed line in a large file must still produce a MINIMAL diff (one
// -/+ pair), proving the common prefix/suffix trim that bounds the LCS matrix
// does not coarsen ordinary edits.
func TestUnified_SingleChangeInLargeFile(t *testing.T) {
	var bb, ab strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&bb, "line-%d\n", i)
		if i == 2500 {
			ab.WriteString("CHANGED\n")
		} else {
			fmt.Fprintf(&ab, "line-%d\n", i)
		}
	}
	got := Unified("x.txt", bb.String(), ab.String())
	if c := countAdds(got); c != 1 {
		t.Fatalf("added content lines = %d, want exactly 1 (minimal diff):\n%s", c, firstLines(got, 12))
	}
	if !strings.Contains(got, "-line-2500\n+CHANGED\n") {
		t.Fatalf("expected minimal single-line change, got:\n%s", firstLines(got, 12))
	}
}

// countAdds counts content additions (lines beginning with "+"), excluding the
// "+++" file header.
func countAdds(diff string) int {
	n := 0
	for line := range strings.SplitSeq(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			n++
		}
	}
	return n
}

// Two large, entirely-different inputs exceed the LCS cell cap, so the diff
// falls back to a coarse delete-all/add-all block. That block must still be
// LOSSLESS: the +lines reconstruct after and the -lines reconstruct before.
func TestUnified_CoarseFallbackIsLossless(t *testing.T) {
	var bb, ab strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&bb, "old-%d\n", i)
		fmt.Fprintf(&ab, "new-%d\n", i)
	}
	before, after := bb.String(), ab.String()
	got := Unified("x.txt", before, after)

	var adds, dels strings.Builder
	for line := range strings.SplitSeq(got, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			fmt.Fprintf(&adds, "%s\n", line[1:])
		case strings.HasPrefix(line, "-"):
			fmt.Fprintf(&dels, "%s\n", line[1:])
		}
	}
	if adds.String() != after {
		t.Fatalf("added lines do not reconstruct after content")
	}
	if dels.String() != before {
		t.Fatalf("deleted lines do not reconstruct before content")
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}

// A pure insertion (empty before) must use git's zero-line-range convention in
// the hunk header: the old side is "-0,0", not "-1,0".
func TestUnified_PureAddUsesZeroRange(t *testing.T) {
	got := Unified("x.txt", "", "alpha\nbeta\n")
	if !strings.Contains(got, "@@ -0,0 +1,2 @@") {
		t.Fatalf("pure-add hunk header wrong, want '@@ -0,0 +1,2 @@':\n%s", got)
	}
}

// A pure deletion (empty after) must likewise use "+0,0" on the new side.
func TestUnified_PureDeleteUsesZeroRange(t *testing.T) {
	got := Unified("x.txt", "alpha\nbeta\n", "")
	if !strings.Contains(got, "@@ -1,2 +0,0 @@") {
		t.Fatalf("pure-delete hunk header wrong, want '@@ -1,2 +0,0 @@':\n%s", got)
	}
}

func TestUnified_NoChangeIsEmpty(t *testing.T) {
	src := "alpha\nbeta\n"
	if got := Unified("x.txt", src, src); got != "" {
		t.Fatalf("Unified of identical text = %q, want empty", got)
	}
}

// TestUnifiedRendersATrailingNewlineOnlyChange pins #178: before and after
// with otherwise-identical content but a differing trailing newline must NOT
// collapse to "" (that would silently hide a real one-byte difference, which
// the package's own Lossless doc comment forbids). The expected rendering
// matches git/GNU diff's own "\ No newline at end of file" convention,
// verified against a real `git diff` on the same before/after content.
func TestUnifiedRendersATrailingNewlineOnlyChange(t *testing.T) {
	got := Unified("x.txt", "a\nb", "a\nb\n")
	want := "--- a/x.txt\n" +
		"+++ b/x.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" a\n" +
		"-b\n" +
		"\\ No newline at end of file\n" +
		"+b\n"
	if got != want {
		t.Fatalf("Unified mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestUnifiedRendersALostTrailingNewline is the reverse direction: after
// drops the trailing newline before had. The marker must move to the "+b"
// side, again matching git's own rendering.
func TestUnifiedRendersALostTrailingNewline(t *testing.T) {
	got := Unified("x.txt", "a\nb\n", "a\nb")
	want := "--- a/x.txt\n" +
		"+++ b/x.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" a\n" +
		"-b\n" +
		"+b\n" +
		"\\ No newline at end of file\n"
	if got != want {
		t.Fatalf("Unified mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestUnifiedIsEmptyWhenBothSidesLackATrailingNewlineAndMatch covers the
// fourth (before-has-newline x after-has-newline) combination the other three
// trailing-newline tests miss: both before and after lack a trailing
// newline AND their content is otherwise identical. That must still collapse
// to "" — before == after byte-for-byte — never render a diff for zero actual
// change, per Unified's own doc comment.
func TestUnifiedIsEmptyWhenBothSidesLackATrailingNewlineAndMatch(t *testing.T) {
	if got := Unified("f.txt", "a", "a"); got != "" {
		t.Fatalf("Unified of identical no-trailing-newline text = %q, want empty", got)
	}
}
