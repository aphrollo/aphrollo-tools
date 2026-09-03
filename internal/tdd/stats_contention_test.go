package tdd

import (
	"strings"
	"testing"
	"time"
)

// A box under contention looks exactly like a box with a slow suite unless
// somebody counts. Three hours of one measured day: hooks waiting up to 619 s
// for a build slot and 56 runs deferred, and finding that out took a hand
// tally of gate.log. The report states it.
func TestRenderGateStats_StatesTheContentionItMeasured(t *testing.T) {
	log := strings.Join([]string{
		"2026-09-02T10:00:00Z postedit D:/repo/crates/a cargo_test lock-wait 619.0s",
		"2026-09-02T10:01:00Z postedit D:/repo/crates/a cargo_test lock-wait 12.0s",
		"2026-09-02T10:02:00Z postedit D:/repo/crates/a cargo_test deferred 110.0s",
		"2026-09-02T10:03:00Z postedit D:/repo/crates/b cargo_test deferred 110.0s",
		"2026-09-02T10:04:00Z postedit D:/repo/crates/b cargo_test queued-skipped 0.1s",
		"2026-09-02T10:05:00Z postedit D:/repo/crates/b cargo_test green 8.0s",
	}, "\n")

	got := RenderGateStats(GateStats(strings.NewReader(log), time.Time{}))
	want := "contention: longest build-slot wait 619s, 2 deferred, 1 queued-skipped"
	if !strings.Contains(got, want) {
		t.Fatalf("stats report has no contention line %q, got:\n%s", want, got)
	}
}

// Zero is a reading, not a blank: a report that only mentions contention when
// there is some cannot be used to say the box was healthy.
func TestRenderGateStats_SaysSoWhenThereWasNoContention(t *testing.T) {
	log := "2026-09-02T10:05:00Z postedit D:/repo/crates/b cargo_test green 8.0s"
	got := RenderGateStats(GateStats(strings.NewReader(log), time.Time{}))
	if !strings.Contains(got, "contention: longest build-slot wait 0s, 0 deferred, 0 queued-skipped") {
		t.Fatalf("stats report hides a quiet box, got:\n%s", got)
	}
}
