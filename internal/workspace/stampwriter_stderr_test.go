package workspace

import (
	"bytes"
	"fmt"

	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

// The gate's stage lines are written to the process's stderr, not to the writer
// the merge wait was handed. Routed through the same stamping writer they carry
// the same UTC time prefix, once, from the same clock, and nothing is lost.
// Not parallel: it swaps os.Stderr.
func TestRouteProcessStderr_StampsTheGatesStageLinesFromTheWaitsClock(t *testing.T) {
	at := func(s int) time.Time { return time.Date(2026, 10, 8, 10, 0, s, 0, time.UTC) }
	orig := waitNow
	waitNow = stepClock(at(0), at(7), at(9))
	t.Cleanup(func() { waitNow = orig })
	before := os.Stderr

	var buf bytes.Buffer
	restore := RouteProcessStderr(StampLines(&buf))
	fmt.Fprintln(os.Stderr, "gate premerge: ratchet → clean (3 law(s), 9 file(s))")
	fmt.Fprintf(rootseam.Stderr(t.TempDir()), "[mechanical] gate premerge: go test in x → green (1.0s)\n")
	fmt.Fprint(os.Stderr, "tail without a newline")
	restore()

	want := "10:00:00 gate premerge: ratchet → clean (3 law(s), 9 file(s))\n" +
		"10:00:07 [mechanical] gate premerge: go test in x → green (1.0s)\n" +
		"10:00:09 tail without a newline"
	if got := buf.String(); got != want {
		t.Errorf("stamped stage lines:\n%q\nwant:\n%q", got, want)
	}
	if os.Stderr != before {
		t.Error("os.Stderr was not put back")
	}
}

// restore runs twice in the merge verb (explicitly, then as a backstop); the
// second must not put back a stderr somebody else has since set.
func TestRouteProcessStderr_RestoreIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	restore := RouteProcessStderr(&buf)
	restore()
	sentinel, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer sentinel.Close()
	orig := os.Stderr
	os.Stderr = sentinel
	defer func() { os.Stderr = orig }()
	restore()
	if os.Stderr != sentinel {
		t.Error("a second restore replaced os.Stderr")
	}
}

// A child that outlives the gate keeps a copy of the pipe's write end, so the
// pipe never reaches EOF. restore must still return within the drain bound,
// on Windows too, where closing an anonymous pipe with a pending read can hang.
func TestRouteProcessStderr_RestoreReturnsWithinTheBoundWhileAChildHoldsThePipe(t *testing.T) {
	origWait := stderrDrainWait
	stderrDrainWait = 200 * time.Millisecond
	t.Cleanup(func() { stderrDrainWait = origWait })

	var buf bytes.Buffer
	restore := RouteProcessStderr(&buf)
	// git hash-object reads its stdin to EOF, so it holds its stderr until the
	// test closes that pipe.
	child := exec.Command("git", "hash-object", "--stdin")
	child.Stderr = os.Stderr
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		restore()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { restore(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("restore did not return while a child held the pipe")
	}
	_ = stdin.Close()
	_ = child.Wait()
}
