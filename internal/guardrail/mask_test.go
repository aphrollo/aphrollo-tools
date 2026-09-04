package guardrail

import "testing"

// TestMask_HashAtIndexZeroBeginsAComment pins the i == 0 disjunct: a `#` as the
// very first byte has no preceding byte to check, so it must still open a
// comment. With no newline in the input, the comment runs to the end of the
// string.
func TestMask_HashAtIndexZeroBeginsAComment(t *testing.T) {
	got := mask("#comment")
	want := "        " // all 8 bytes blanked
	if got != want {
		t.Fatalf("mask(%q) = %q, want %q", "#comment", got, want)
	}
}

// TestMask_HashPrecededBySpaceStartsAComment pins both the leading `c == '#'`
// test and the space disjunct's `b[i-1]` index: the text before the space is
// left alone and the `#` plus everything after it is blanked.
func TestMask_HashPrecededBySpaceStartsAComment(t *testing.T) {
	got := mask("abc #def")
	want := "abc     " // "abc " kept, "#def" (4 bytes) blanked
	if got != want {
		t.Fatalf("mask(%q) = %q, want %q", "abc #def", got, want)
	}
}

// TestMask_HashPrecededByTabStartsAComment pins the tab disjunct's `b[i-1]`
// index the same way the space case pins the space disjunct.
func TestMask_HashPrecededByTabStartsAComment(t *testing.T) {
	got := mask("abc\t#def")
	want := "abc\t    " // "abc\t" kept, "#def" (4 bytes) blanked
	if got != want {
		t.Fatalf("mask(%q) = %q, want %q", "abc\t#def", got, want)
	}
}

// TestMask_HashPrecededByNewlineStartsAComment pins the newline disjunct's
// `b[i-1]` index: a `#` that opens a new line (rather than sitting at index 0)
// still starts a comment.
func TestMask_HashPrecededByNewlineStartsAComment(t *testing.T) {
	got := mask("line1\n#comment")
	want := "line1\n        " // "line1\n" kept, "#comment" (8 bytes) blanked
	if got != want {
		t.Fatalf("mask(%q) = %q, want %q", "line1\n#comment", got, want)
	}
}

// TestMask_HashInsideAWordIsNotAComment is the negative control for all four
// disjuncts at once: a `#` preceded by an ordinary letter, not at index 0,
// must be left untouched — it is part of a word, not a comment marker.
func TestMask_HashInsideAWordIsNotAComment(t *testing.T) {
	got := mask("abc#def")
	want := "abc#def"
	if got != want {
		t.Fatalf("mask(%q) = %q, want %q (unchanged)", "abc#def", got, want)
	}
}

// TestMask_HashInsideDoubleQuotesIsNotAComment proves the quote states take
// priority over the comment case: a `#` between double quotes is blanked as
// ordinary quoted content, never treated as a comment opener, and the quote
// characters themselves survive.
func TestMask_HashInsideDoubleQuotesIsNotAComment(t *testing.T) {
	got := mask(`echo "a#b" c`)
	want := `echo "   " c`
	if got != want {
		t.Fatalf("mask(%q) = %q, want %q", `echo "a#b" c`, got, want)
	}
}

// TestMask_HashInsideSingleQuotesIsNotAComment is the single-quote twin of the
// double-quote case above.
func TestMask_HashInsideSingleQuotesIsNotAComment(t *testing.T) {
	got := mask(`echo 'a#b' c`)
	want := `echo '   ' c`
	if got != want {
		t.Fatalf("mask(%q) = %q, want %q", `echo 'a#b' c`, got, want)
	}
}
