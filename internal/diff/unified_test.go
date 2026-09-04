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

// The tests below call markMissingTrailingNewline directly (it is unexported,
// same package) rather than through Unified, so each pins exactly one shape of
// its ops slice instead of reverse-engineering before/after text that happens
// to produce it.

// TestMarkMissingTrailingNewline_SameTrailingNewlineStatusIsANoOp pins the
// beforeNL == afterNL half of the early return: when both sides agree on
// trailing-newline presence there is nothing to annotate, even though the ops
// themselves are non-empty.
func TestMarkMissingTrailingNewline_SameTrailingNewlineStatusIsANoOp(t *testing.T) {
	ops := []op{{kind: ' ', line: "x", oldPos: 1, newPos: 1}}
	got := markMissingTrailingNewline(ops, true, true)
	if len(got) != 1 || got[0].noNL {
		t.Fatalf("markMissingTrailingNewline with matching newline flags = %+v, want the input op untouched", got)
	}
}

// TestMarkMissingTrailingNewline_EmptyOpsIsANoOp pins the len(ops) == 0 half of
// the early return: even with disagreeing newline flags, an empty diff has
// nothing to split or annotate.
func TestMarkMissingTrailingNewline_EmptyOpsIsANoOp(t *testing.T) {
	got := markMissingTrailingNewline(nil, false, true)
	if len(got) != 0 {
		t.Fatalf("markMissingTrailingNewline(nil, false, true) = %+v, want empty", got)
	}
}

// TestMarkMissingTrailingNewline_PureInsertionLeavesOldSideUntouched pins
// lastOld's -1 initial value: when every op is an insertion ('+'), the loop's
// `o.kind != '+'` guard never assigns lastOld, so it must stay -1 and never
// index into ops. Any other initial value here would incorrectly satisfy one
// of the >= 0 guards below and mutate an op that owns no "old" line.
func TestMarkMissingTrailingNewline_PureInsertionLeavesOldSideUntouched(t *testing.T) {
	ops := []op{
		{kind: '+', line: "a", oldPos: 1, newPos: 1},
		{kind: '+', line: "b", oldPos: 1, newPos: 2},
		{kind: '+', line: "c", oldPos: 1, newPos: 3},
	}
	got := markMissingTrailingNewline(ops, false, true)
	if len(got) != 3 {
		t.Fatalf("markMissingTrailingNewline split a pure insertion: got %d ops, want 3", len(got))
	}
	for i, o := range got {
		if o.noNL || o.kind != '+' {
			t.Fatalf("op[%d] = %+v, want kind '+' and noNL false (old side has no line to annotate)", i, o)
		}
	}
}

// TestMarkMissingTrailingNewline_PureDeletionLeavesNewSideUntouched is the
// symmetric case pinning lastNew's -1 initial value via an all-'-' ops slice.
func TestMarkMissingTrailingNewline_PureDeletionLeavesNewSideUntouched(t *testing.T) {
	ops := []op{
		{kind: '-', line: "a", oldPos: 1, newPos: 1},
		{kind: '-', line: "b", oldPos: 2, newPos: 1},
		{kind: '-', line: "c", oldPos: 3, newPos: 1},
	}
	got := markMissingTrailingNewline(ops, true, false)
	if len(got) != 3 {
		t.Fatalf("markMissingTrailingNewline split a pure deletion: got %d ops, want 3", len(got))
	}
	for i, o := range got {
		if o.noNL || o.kind != '-' {
			t.Fatalf("op[%d] = %+v, want kind '-' and noNL false (new side has no line to annotate)", i, o)
		}
	}
}

// TestMarkMissingTrailingNewline_SplitsTheSharedLastLineWhenBothSidesAgreeOnItsIndex
// pins the lastOld == 0 boundary of `lastOld >= 0 && lastOld == lastNew`: a
// single shared context op is both files' last line, so it must split into a
// -/+ pair each carrying its own noNL — not just get noNL set in place.
func TestMarkMissingTrailingNewline_SplitsTheSharedLastLineWhenBothSidesAgreeOnItsIndex(t *testing.T) {
	ops := []op{{kind: ' ', line: "x", oldPos: 5, newPos: 7}}
	got := markMissingTrailingNewline(ops, false, true)
	want := []op{
		{kind: '-', line: "x", oldPos: 5, newPos: 7, noNL: true},
		{kind: '+', line: "x", oldPos: 5, newPos: 7, noNL: false},
	}
	if len(got) != len(want) {
		t.Fatalf("markMissingTrailingNewline split = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("markMissingTrailingNewline split[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestMarkMissingTrailingNewline_MarksTheOldSideNoNLWhenTheLastLinesDiffer pins
// the non-split branch: when the old side's last op and the new side's last op
// are NOT the same index (here a trailing insertion follows the shared line),
// only the old side's op gets noNL, in place, with no split.
func TestMarkMissingTrailingNewline_MarksTheOldSideNoNLWhenTheLastLinesDiffer(t *testing.T) {
	ops := []op{
		{kind: ' ', line: "x", oldPos: 1, newPos: 1},
		{kind: '+', line: "y", oldPos: 1, newPos: 2},
	}
	got := markMissingTrailingNewline(ops, false, true)
	if len(got) != 2 {
		t.Fatalf("markMissingTrailingNewline changed op count: got %d, want 2 (no split)", len(got))
	}
	if !got[0].noNL || got[0].kind != ' ' {
		t.Fatalf("op[0] = %+v, want kind ' ' with noNL true", got[0])
	}
	if got[1].noNL || got[1].kind != '+' {
		t.Fatalf("op[1] = %+v, want kind '+' with noNL false", got[1])
	}
}
