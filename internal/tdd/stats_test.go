package tdd

import (
	"strings"
	"testing"
	"time"
)

// TestGateStats_TalliesTheLogByStageAndOutcome pins the point of the command:
// pipeline health should be a number, not a feeling. Before this the only way
// to know how often the gate timed out, queued or deferred was to read
// thousands of gate.log lines by eye.
func TestGateStats_TalliesTheLogByStageAndOutcome(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	log := strings.Join([]string{
		stamp(now.Add(-30*time.Minute), "postedit", `D:\repo\crates\server`, "cargo nextest run -p server", "green", 12.5),
		stamp(now.Add(-25*time.Minute), "postedit", `D:\repo\crates\server`, "cargo nextest run -p server", "timeout", 110),
		stamp(now.Add(-20*time.Minute), "postedit", `D:\repo\crates\pose`, "cargo nextest run -p pose", "deferred", 0),
		stamp(now.Add(-10*time.Minute), "precommit", `D:\repo`, "cargo nextest run -p server", "green", 60),
		stamp(now.Add(-5*time.Minute), "precommit", `D:\repo`, "cargo nextest run -p server", "timeout-rejected", 600),
		stamp(now.Add(-72*time.Hour), "precommit", `D:\repo`, "cargo nextest run -p server", "green", 1),
	}, "")

	got := GateStats(strings.NewReader(log), now.Add(-24*time.Hour))

	if n := got.Count("postedit", "green"); n != 1 {
		t.Errorf("postedit green = %d, want 1", n)
	}
	if n := got.Count("precommit", "green"); n != 1 {
		t.Errorf("precommit green = %d, want 1 — the 72h-old line is outside --since", n)
	}
	if n := got.Count("precommit", "timeout-rejected"); n != 1 {
		t.Errorf("precommit timeout-rejected = %d, want 1", n)
	}
	if n := got.Timeouts["server"]; n != 1 {
		t.Errorf("server timeouts = %d, want 1 (per-crate, from the root's last path element)", n)
	}
	if n := got.Deferred["pose"]; n != 1 {
		t.Errorf("pose deferred = %d, want 1", n)
	}
	if got.Max != 600 {
		t.Errorf("max seconds = %v, want 600", got.Max)
	}
	if got.Median != 60 {
		t.Errorf("median seconds = %v, want 60 (of 12.5, 110, 0, 60, 600)", got.Median)
	}
}

// TestGateStats_WithoutASinceCoversTheWholeLog pins the default: an operator
// asking "how is the pipeline doing" with no window means all of it.
func TestGateStats_WithoutASinceCoversTheWholeLog(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	log := stamp(old, "precommit", `D:\repo`, "go test ./...", "green", 3)

	got := GateStats(strings.NewReader(log), time.Time{})
	if n := got.Count("precommit", "green"); n != 1 {
		t.Fatalf("green = %d, want the whole log counted when no window is given", n)
	}
}

// TestRenderGateStats_NamesEveryColumnItCounted keeps the table readable: a
// stage with no rows still appears, so "zero timeouts" and "never ran" are
// not the same blank.
func TestRenderGateStats_NamesEveryColumnItCounted(t *testing.T) {
	now := time.Now().UTC()
	log := stamp(now, "postedit", `D:\repo\crates\pose`, "cargo nextest run -p pose", "queued-skipped", 0)
	out := RenderGateStats(GateStats(strings.NewReader(log), time.Time{}))
	for _, want := range []string{"postedit", "precommit", "queued-skipped", "green"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table is missing %q:\n%s", want, out)
		}
	}
}

// stamp writes one gate.log line in the format appendGateLog produces.
func stamp(at time.Time, stage, root, cmd, verdict string, secs float64) string {
	return at.UTC().Format(time.RFC3339) + " " + stage + " " + root + " " + cmd + " " + verdict + " " +
		formatFloat(secs) + "s\n"
}
