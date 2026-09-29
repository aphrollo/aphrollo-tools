package suite

import (
	"reflect"
	"sort"
	"testing"
)

// These are suite's own tests of zigtest.go, reached today only through
// internal/tdd/postedit's edit-time smell tests.

func zigLines(m map[int]bool) []int {
	var out []int
	for n := range m {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// TestZigTestLines_CoversAMultiLineBlockFromOpenerToCloser pins the span:
// every line from the `test "..." {` line through the closing `}` line.
func TestZigTestLines_CoversAMultiLineBlockFromOpenerToCloser(t *testing.T) {
	t.Parallel()
	src := "const x = 1;\ntest \"adds\" {\n    try expect(1 + 1 == 2);\n}\nconst y = 2;\n"
	if got, want := zigLines(zigTestLines(src)), []int{2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

// TestZigTestLines_NestedBracesCloseTheRightBlock pins the brace matching: an
// inner block's closer does not end the test block.
func TestZigTestLines_NestedBracesCloseTheRightBlock(t *testing.T) {
	t.Parallel()
	src := "test \"nested\" {\n    if (a) {\n        b();\n    }\n    c();\n}\nfn after() void {}\n"
	if got, want := zigLines(zigTestLines(src)), []int{1, 2, 3, 4, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

// TestZigTestLines_ASingleLineBlockKeepsItsOwnLine pins the inclusive ends.
func TestZigTestLines_ASingleLineBlockKeepsItsOwnLine(t *testing.T) {
	t.Parallel()
	if got, want := zigLines(zigTestLines("test \"x\" { try f(); }\n")), []int{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

// TestZigTestLines_TwoBlocksAreBothCovered pins that the scan continues past
// the first block.
func TestZigTestLines_TwoBlocksAreBothCovered(t *testing.T) {
	t.Parallel()
	src := "test \"a\" {\n}\nconst gap = 0;\ntest b {\n}\n"
	if got, want := zigLines(zigTestLines(src)), []int{1, 2, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

// TestZigTestLines_AnUnclosedBlockIsSkipped pins the unbalanced arm: a block
// that never closes contributes nothing rather than the rest of the file.
func TestZigTestLines_AnUnclosedBlockIsSkipped(t *testing.T) {
	t.Parallel()
	if got := zigTestLines("test \"open\" {\n    still going\n"); len(got) != 0 {
		t.Fatalf("lines = %v, want none", zigLines(got))
	}
}

// TestZigTestLines_TheWordTestInsideAnIdentifierIsNotABlock pins the leading
// boundary: `latest {` and `x.test {` are not test blocks.
func TestZigTestLines_TheWordTestInsideAnIdentifierIsNotABlock(t *testing.T) {
	t.Parallel()
	if got := zigTestLines("const latest = struct {\n};\nconst x = y.test {\n};\n"); len(got) != 0 {
		t.Fatalf("lines = %v, want none", zigLines(got))
	}
}

// TestMatchBrace_ReturnsTheIndexOfTheMatchingCloser pins the matcher on its
// own: depth counting, the return index, and -1 for an unbalanced string.
func TestMatchBrace_ReturnsTheIndexOfTheMatchingCloser(t *testing.T) {
	t.Parallel()
	s := "{ a { b } c }"
	if got := matchBrace(s, 0); got != len(s)-1 {
		t.Errorf("matchBrace(outer) = %d, want %d", got, len(s)-1)
	}
	if got := matchBrace(s, 4); got != 8 {
		t.Errorf("matchBrace(inner) = %d, want 8", got)
	}
	if got := matchBrace("{ never closes", 0); got != -1 {
		t.Errorf("matchBrace(unclosed) = %d, want -1", got)
	}
}

// TestLineOf_IsOneBasedByNewlineCount pins the line arithmetic.
func TestLineOf_IsOneBasedByNewlineCount(t *testing.T) {
	t.Parallel()
	s := "a\nbb\nccc"
	for idx, want := range map[int]int{0: 1, 1: 1, 2: 2, 5: 3} {
		if got := lineOf(s, idx); got != want {
			t.Errorf("lineOf(%d) = %d, want %d", idx, got, want)
		}
	}
}
