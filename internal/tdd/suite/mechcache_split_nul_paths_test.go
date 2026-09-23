package suite

import "testing"

// TestSplitNulPaths_DropsOnlyTheTrailingEmptyField pins the shape a `-z`
// git list actually has: every path, including the last, carries a
// terminating NUL, so splitting on it leaves one empty trailing field that
// must be dropped — never left for a caller to join against a base
// directory and stamp the base itself "gone".
func TestSplitNulPaths_DropsOnlyTheTrailingEmptyField(t *testing.T) {
	t.Parallel()
	got := splitNulPaths("a\x00b\x00")
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("splitNulPaths(%q) = %v, want %v", "a\x00b\x00", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitNulPaths(%q) = %v, want %v", "a\x00b\x00", got, want)
		}
	}
}

// TestSplitNulPaths_KeepsANonEmptyLastFieldIntact pins the other side of the
// same decision: input NOT terminated by NUL must not lose its real last
// path. A mutant that strips whenever the last field is non-empty (instead
// of when it is empty) would drop "b" here.
func TestSplitNulPaths_KeepsANonEmptyLastFieldIntact(t *testing.T) {
	t.Parallel()
	got := splitNulPaths("a\x00b")
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("splitNulPaths(%q) = %v, want %v", "a\x00b", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitNulPaths(%q) = %v, want %v", "a\x00b", got, want)
		}
	}
}

// TestSplitNulPaths_SinglePathTerminatedByNulYieldsOneEntry pins the
// smallest input that reaches the trim: a single path plus its terminator
// must yield exactly that one path, not an empty set and not two entries.
func TestSplitNulPaths_SinglePathTerminatedByNulYieldsOneEntry(t *testing.T) {
	t.Parallel()
	got := splitNulPaths("a\x00")
	want := []string{"a"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("splitNulPaths(%q) = %v, want %v", "a\x00", got, want)
	}
}
