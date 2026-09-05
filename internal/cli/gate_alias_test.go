package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestGatePremerge_IsTheSameRoutineAsPremergecommit proves "premerge" is not
// a second copy of the merge gate that happens to agree with the pre-rename
// "premergecommit" — both spellings run through the identical routine, and
// every line it prints now starts with the new name.
func TestGatePremerge_IsTheSameRoutineAsPremergecommit(t *testing.T) {
	gateConfigDir(t)
	var calls []string
	restore := premergeRoutineSeam
	premergeRoutineSeam = func(routine string) { calls = append(calls, routine) }
	t.Cleanup(func() { premergeRoutineSeam = restore })

	commitRepo(t)

	var out1, err1 bytes.Buffer
	if code := Run([]string{"gate", "premergecommit"}, strings.NewReader(""), &out1, &err1); code != 0 {
		t.Fatalf("premergecommit exit = %d\nstderr: %s", code, err1.String())
	}
	var out2, err2 bytes.Buffer
	if code := Run([]string{"gate", "premerge"}, strings.NewReader(""), &out2, &err2); code != 0 {
		t.Fatalf("premerge exit = %d\nstderr: %s", code, err2.String())
	}

	if len(calls) != 2 || calls[0] != calls[1] {
		t.Fatalf("both spellings must reach the identical routine, got %v", calls)
	}
	for _, s := range []string{err1.String(), err2.String()} {
		if !strings.HasPrefix(s, "gate premerge:") {
			t.Fatalf("expected the routine's first line to start %q, got %q", "gate premerge:", s)
		}
	}
}
