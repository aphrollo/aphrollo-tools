package run

import (
	"slices"
	"strings"
	"testing"
)

func TestSplit_KeepsTheLineWithinTheBudgetAndEveryPathOnce(t *testing.T) {
	files := []string{"aaaa", "bbbb", "cccc", "dddd"}

	got := Split([]string{"go", "vet"}, files, 14)

	want := [][]string{{"go", "vet", "aaaa"}, {"go", "vet", "bbbb"}, {"go", "vet", "cccc"}, {"go", "vet", "dddd"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("Split = %v, want %v", got, want)
	}
}

func TestBatch_RunsOncePerBatchAndJoinsTheOutputsInOrder(t *testing.T) {
	paths := []string{strings.Repeat("a", Budget-5), strings.Repeat("b", Budget-5)}
	var calls int

	out, err := Batch([]string{"x"}, paths, func(args []string) (string, error) {
		calls++
		return args[len(args)-1][:1], nil
	})

	if err != nil || out != "ab" || calls != 2 {
		t.Fatalf("Batch = %q, %v after %d calls; want \"ab\", nil after 2", out, err, calls)
	}
}
