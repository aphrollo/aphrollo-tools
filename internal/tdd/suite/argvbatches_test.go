package suite

import (
	"slices"
	"strings"
	"testing"
)

// TestArgvBatches_FillsEachBatchUpToTheBudgetInclusive pins where a batch
// ends: a file that brings the line to exactly the budget stays in it, one
// that would pass it starts the next batch, which repeats the prefix.
func TestArgvBatches_FillsEachBatchUpToTheBudgetInclusive(t *testing.T) {
	prefix := []string{"--format", "json"} // "--format json" is 13 chars
	// 13 + " aaaa" + " bbbb" = 23 chars; " cccc" would make 28.
	files := []string{"aaaa", "bbbb", "cccc", "dddd"}

	got := argvBatches(prefix, files, 23)

	want := [][]string{
		{"--format", "json", "aaaa", "bbbb"},
		{"--format", "json", "cccc", "dddd"},
	}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("argvBatches = %q, want %q", got, want)
	}
	for _, b := range got {
		if n := len(strings.Join(b, " ")); n > 23 {
			t.Fatalf("batch %q is %d chars, past the 23-char budget", b, n)
		}
	}
}

// TestArgvBatches_OneFileOverTheBudgetStillRunsAlone pins that no file is
// dropped: a single path longer than the whole budget gets a batch of its
// own, the shortest line that can lint it.
func TestArgvBatches_OneFileOverTheBudgetStillRunsAlone(t *testing.T) {
	long := strings.Repeat("p", 40)

	got := argvBatches([]string{"-x"}, []string{"a", long, "b"}, 10)

	want := [][]string{{"-x", "a"}, {"-x", long}, {"-x", "b"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("argvBatches = %q, want %q", got, want)
	}
}

// TestArgvBatches_NoFilesIsNoBatch pins that an empty changed set runs
// nothing rather than a bare prefix, which would lint the tool's default.
func TestArgvBatches_NoFilesIsNoBatch(t *testing.T) {
	if got := argvBatches([]string{"--format", "json"}, nil, 100); got != nil {
		t.Fatalf("argvBatches with no files = %q, want nil", got)
	}
}

// TestArgvBatches_OneCharPastTheBudgetStartsANewBatch is the other side of
// the edge: a file that would bring the line to one character over the
// budget goes to the next batch.
func TestArgvBatches_OneCharPastTheBudgetStartsANewBatch(t *testing.T) {
	// "--format json aaaa" is 18 chars; " bbbb" would make 23, one past 22.
	got := argvBatches([]string{"--format", "json"}, []string{"aaaa", "bbbb"}, 22)

	want := [][]string{{"--format", "json", "aaaa"}, {"--format", "json", "bbbb"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("argvBatches = %q, want %q", got, want)
	}
}
