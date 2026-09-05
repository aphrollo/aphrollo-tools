package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// TestGatePremerge_IsTheSameRoutineAsPremergecommit proves "premerge" is not
// a second copy of the merge gate that happens to agree with the pre-rename
// "premergecommit" — both spellings run through the identical routine, and
// every line it prints now starts with the new name. This has to read the
// REAL os.Stderr, not just the writer Run is given: several stage functions
// inside the routine print straight to os.Stderr with the literal gate name,
// bypassing the writer entirely, and a rewrite that only touched the joined
// GateResult.Message left those lines unrenamed.
func TestGatePremerge_IsTheSameRoutineAsPremergecommit(t *testing.T) {
	gateConfigDir(t)
	var calls []string
	restore := premergeRoutineSeam
	premergeRoutineSeam = func(routine string) { calls = append(calls, routine) }
	t.Cleanup(func() { premergeRoutineSeam = restore })

	commitRepo(t)

	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("os.Pipe: %v", perr)
	}
	origStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })
	captured := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		captured <- buf.String()
	}()

	var out1, err1 bytes.Buffer
	if code := Run([]string{"gate", "premergecommit"}, strings.NewReader(""), &out1, &err1); code != 0 {
		w.Close()
		os.Stderr = origStderr
		t.Fatalf("premergecommit exit = %d\nstderr: %s", code, err1.String())
	}
	var out2, err2 bytes.Buffer
	if code := Run([]string{"gate", "premerge"}, strings.NewReader(""), &out2, &err2); code != 0 {
		w.Close()
		os.Stderr = origStderr
		t.Fatalf("premerge exit = %d\nstderr: %s", code, err2.String())
	}

	w.Close()
	os.Stderr = origStderr
	real := <-captured

	if len(calls) != 2 || calls[0] != calls[1] {
		t.Fatalf("both spellings must reach the identical routine, got %v", calls)
	}
	sawPremerge := false
	for _, line := range strings.Split(real, "\n") {
		if strings.HasPrefix(line, "gate premergecommit:") {
			t.Fatalf("real stderr still carries the pre-rename name: %q\nfull output:\n%s", line, real)
		}
		if strings.HasPrefix(line, "gate premerge:") {
			sawPremerge = true
		}
	}
	if !sawPremerge {
		t.Fatalf("expected at least one real-stderr line to start %q, got:\n%s", "gate premerge:", real)
	}
}
