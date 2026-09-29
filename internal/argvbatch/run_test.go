package argvbatch

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestRun_OnePerBatchOutputsJoinedInOrder pins the contract every git call
// site leans on: a path list past the budget runs as several calls, each
// within it, and the caller reads their outputs as one, in path order.
func TestRun_OnePerBatchOutputsJoinedInOrder(t *testing.T) {
	var paths []string
	for i := range 3 * Budget / 100 {
		paths = append(paths, strings.Repeat(string(rune('a'+i%26)), 99))
	}
	var calls [][]string
	got, err := Run([]string{"ls-files", "--"}, paths, func(args []string) (string, error) {
		calls = append(calls, args)
		return strings.Join(args[2:], "\n") + "\n", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Join(paths, "\n") + "\n"; got != want {
		t.Fatalf("joined output differs from one call over every path:\ngot  %.80q…\nwant %.80q…", got, want)
	}
	if len(calls) < 3 {
		t.Fatalf("%d calls for %d paths of 99 chars, want the list split at the %d-char budget", len(calls), len(paths), Budget)
	}
	for i, args := range calls {
		if !slices.Equal(args[:2], []string{"ls-files", "--"}) {
			t.Errorf("call %d lost its prefix: %q", i, args[:2])
		}
		if n := len(strings.Join(args, " ")); n > Budget {
			t.Errorf("call %d is %d chars, past the %d-char budget", i, n, Budget)
		}
	}
}

// TestRun_NoPathsRunsThePrefixOnce pins that an empty list is the call a
// caller made before batching: `ls-files --` with no pathspec lists the
// whole index, and skipping the call would list nothing.
func TestRun_NoPathsRunsThePrefixOnce(t *testing.T) {
	var calls [][]string
	got, err := Run([]string{"ls-files"}, nil, func(args []string) (string, error) {
		calls = append(calls, args)
		return "all\n", nil
	})
	if err != nil || got != "all\n" {
		t.Fatalf("Run = %q, %v; want the one call's output", got, err)
	}
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"ls-files"}) {
		t.Fatalf("calls = %q, want exactly [[ls-files]]", calls)
	}
}

// TestRun_StopsAtTheFirstFailingBatch pins that a failure is never folded
// into a partial answer: the failing call's own output and error come back,
// and no later batch runs.
func TestRun_StopsAtTheFirstFailingBatch(t *testing.T) {
	paths := []string{strings.Repeat("a", Budget), strings.Repeat("b", Budget), strings.Repeat("c", Budget)}
	boom := errors.New("exit status 128")
	calls := 0
	got, err := Run(nil, paths, func(args []string) (string, error) {
		calls++
		if calls == 2 {
			return "fatal: bad pathspec\n", boom
		}
		return "ok\n", nil
	})
	if !errors.Is(err, boom) || got != "fatal: bad pathspec\n" {
		t.Fatalf("Run = %q, %v; want the failing batch's output and error", got, err)
	}
	if calls != 2 {
		t.Fatalf("%d calls, want the run to stop at the failing second", calls)
	}
}
