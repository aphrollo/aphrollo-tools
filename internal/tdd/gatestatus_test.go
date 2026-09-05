package tdd

import (
	"strings"
	"testing"
	"time"
)

// TestFormatGateStatus_ReportsAllThreeSources is the shape acceptance names
// for issue #430: one deferred job, one held build slot, one idle slot, and
// a mutation state, all printed in one report.
func TestFormatGateStatus_ReportsAllThreeSources(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	jobs := []DeferredJob{
		{Project: "/repo/a", Phase: "build", PID: 123, Started: now.Add(-30 * time.Second)},
	}
	slots := []BuildSlotStatus{
		{Index: 0, Held: true, Owner: BuildLockOwner{Cmd: "cargo test", Cwd: "/repo/b", PID: 456, Started: now.Add(-10 * time.Second)}},
		{Index: 1, Held: false},
	}
	mutants := MutantsStatusReport{State: MutantsRunNone, Branch: "lane/x", TipTree: "deadbeefcafe"}

	out := FormatGateStatus(jobs, slots, mutants, nil, now)

	if !strings.Contains(out, "/repo/a") || !strings.Contains(out, "[build]") ||
		!strings.Contains(out, "pid 123") || !strings.Contains(out, "30s") {
		t.Errorf("missing deferred job line, got:\n%s", out)
	}
	if !strings.Contains(out, "held by") || !strings.Contains(out, "cargo test") || !strings.Contains(out, "/repo/b") {
		t.Errorf("missing held-slot line, got:\n%s", out)
	}
	if !strings.Contains(out, "slot 1: idle") {
		t.Errorf("missing idle-slot line, got:\n%s", out)
	}
	if !strings.Contains(out, "no run has been started for lane/x") {
		t.Errorf("missing mutation-status line, got:\n%s", out)
	}
}

// TestFormatGateStatus_NoDeferredJobsSaysSo: an empty job list must read as
// "checked, found nothing", not as a blank the reader has to interpret.
func TestFormatGateStatus_NoDeferredJobsSaysSo(t *testing.T) {
	out := FormatGateStatus(nil, nil, MutantsStatusReport{State: MutantsRunNone}, nil, time.Now())
	if !strings.Contains(out, "none running") {
		t.Errorf("expected an explicit 'none running' line, got:\n%s", out)
	}
}
