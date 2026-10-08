package workspace

import (
	"bytes"
	"testing"
	"time"
)

// stepClock answers each reading with the next of ts, then the last forever.
func stepClock(ts ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		t := ts[min(i, len(ts)-1)]
		i++
		return t
	}
}

// Each line of a merge wait's output starts with the UTC time it began, once,
// however the writer was fed: a line written in pieces is stamped at its
// first piece, and a blank line is stamped too. Nothing else changes.
func TestStampLines_PrefixesEachLineOnceAtItsStart(t *testing.T) {
	// 12:00:00 at +02:00 is 10:00:00 UTC: the stamp is UTC, not local.
	zone := time.FixedZone("x", 2*3600)
	at := func(h, m, s int) time.Time { return time.Date(2026, 10, 8, h, m, s, 0, zone) }
	orig := waitNow
	waitNow = stepClock(at(12, 0, 0), at(12, 0, 5), at(12, 0, 9), at(12, 1, 0))
	t.Cleanup(func() { waitNow = orig })

	var buf bytes.Buffer
	w := StampLines(&buf)
	for _, piece := range []string{"  [wait] PR #5", " ok\nsecond\n", "\n", "tail"} {
		n, err := w.Write([]byte(piece))
		if err != nil || n != len(piece) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", piece, n, err, len(piece))
		}
	}
	want := "10:00:00   [wait] PR #5 ok\n" +
		"10:00:05 second\n" +
		"10:00:09 \n" +
		"10:01:00 tail"
	if got := buf.String(); got != want {
		t.Errorf("stamped output:\n%q\nwant:\n%q", got, want)
	}
}
