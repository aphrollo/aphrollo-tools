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

	out := FormatGateStatus(jobs, slots, nil, mutants, nil, now)

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
	out := FormatGateStatus(nil, nil, nil, MutantsStatusReport{State: MutantsRunNone}, nil, time.Now())
	if !strings.Contains(out, "none running") {
		t.Errorf("expected an explicit 'none running' line, got:\n%s", out)
	}
}

// TestFormatGateStatus_NotQueuedSaysSo: an empty waiter list must read as
// "checked, found nothing" the same way the deferred-jobs section does,
// never a silently absent section.
func TestFormatGateStatus_NotQueuedSaysSo(t *testing.T) {
	out := FormatGateStatus(nil, nil, nil, MutantsStatusReport{State: MutantsRunNone}, nil, time.Now())
	if !strings.Contains(out, "queue (this checkout):\n  not queued") {
		t.Errorf("expected an explicit 'not queued' line, got:\n%s", out)
	}
}

// TestFormatGateStatus_ReportsQueueWaiter pins issue #435's stated
// residual: a caller queued behind the cargo shim's lock is named in the
// report, with who it is queued behind.
func TestFormatGateStatus_ReportsQueueWaiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	restore := SetLockDirForTest(t.TempDir())
	defer restore()
	slots := []BuildSlotStatus{
		{Index: 0, Held: true, Owner: BuildLockOwner{Cmd: "cargo test", Cwd: "/repo/b", PID: 456, Started: now.Add(-10 * time.Second)}},
	}
	target := "/repo/a/target"
	remove := WriteQueueWaiter(target, "cargo build -p server", "/repo/a")
	defer remove()
	waiters := QueueWaitersForRoot("/repo/a")

	out := FormatGateStatus(nil, slots, waiters, MutantsStatusReport{State: MutantsRunNone}, nil, now)

	if !strings.Contains(out, `"cargo build -p server" queued`) || !strings.Contains(out, target) {
		t.Errorf("expected the queue section to name the queued command and its target, got:\n%s", out)
	}
}
