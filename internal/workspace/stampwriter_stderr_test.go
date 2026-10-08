package workspace

import (
	"bytes"
	"fmt"
	"os"
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
