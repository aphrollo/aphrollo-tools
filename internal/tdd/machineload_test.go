package tdd

import (
	"strings"
	"testing"
)

// TestForeignProcs_ExcludesADescendantOfSelf pins the direction the issue
// warns is easiest to get backwards (#526): a process this gate invocation
// spawned, however many generations down, must never show up as "foreign
// load" — reporting the gate's own work as an outside culprit is worse than
// reporting nothing.
func TestForeignProcs_ExcludesADescendantOfSelf(t *testing.T) {
	const self = 100
	all := []procSample{
		{pid: self, ppid: 1, name: "aphrollo.exe"},
		{pid: 200, ppid: self, name: "go.exe"},     // direct child
		{pid: 300, ppid: 200, name: "go_test.exe"}, // grandchild
		{pid: 999, ppid: 1, name: "find.exe"},      // unrelated
	}
	got := foreignProcs(self, all)
	for _, p := range got {
		if p.pid == self || p.pid == 200 || p.pid == 300 {
			t.Errorf("foreignProcs(%d, ...) included pid %d (%s), a descendant of self — got %+v", self, p.pid, p.name, got)
		}
	}
}

// TestForeignProcs_IncludesANonDescendant is the other half of the same
// proof: a filter that excludes everything looks identical, in a green run,
// to one that correctly excludes only self's own tree. This pins that a real
// stranger survives the filter.
func TestForeignProcs_IncludesANonDescendant(t *testing.T) {
	const self = 100
	all := []procSample{
		{pid: self, ppid: 1, name: "aphrollo.exe"},
		{pid: 200, ppid: self, name: "go.exe"},
		{pid: 999, ppid: 1, name: "find.exe"},
	}
	got := foreignProcs(self, all)
	found := false
	for _, p := range got {
		if p.pid == 999 {
			found = true
		}
	}
	if !found {
		t.Errorf("foreignProcs(%d, ...) = %+v, want pid 999 (find.exe, unrelated to self) included", self, got)
	}
}

// TestIsSelfOrDescendant_BoundedAgainstCyclicParents guards the walk itself:
// a snapshot torn mid-read (or simply wrong) could hand back a parent chain
// that cycles. Sampling runs on a box already in trouble — an infinite loop
// here would hang the very rejection path it is meant to make more useful.
// No local timer wraps this call on purpose (the gate itself already scans
// test files for real-time waits): a regression that removes the depth
// bound hangs this test, and the suite's own run budget is what catches it.
func TestIsSelfOrDescendant_BoundedAgainstCyclicParents(t *testing.T) {
	parents := map[int]int{1: 2, 2: 1} // 1 and 2 are each other's "parent"
	if got := isSelfOrDescendant(999, 1, parents); got {
		t.Errorf("isSelfOrDescendant(999, 1, cyclic parents) = true, want false — 999 never appears in the cycle")
	}
}

// TestFormatMachineLoad_RendersBoxLoadAndTopForeignProcesses pins the shape
// a reader sees: the box line always renders, and each foreign process gets
// its own "foreign load:" line naming image, pid, current share of one core,
// and cumulative CPU time.
func TestFormatMachineLoad_RendersBoxLoadAndTopForeignProcesses(t *testing.T) {
	top := []procSample{
		{pid: 21768, name: "find.exe", pctOneCore: 99, cpuHours: 4.5},
		{pid: 33356, name: "find.exe", pctOneCore: 99, cpuHours: 4.0},
	}
	got := formatMachineLoad(24, 61, top)
	want := "box: 24 cores, load 61%\n" +
		"foreign load: find.exe (pid 21768) 99% of one core, 4.5h CPU\n" +
		"foreign load: find.exe (pid 33356) 99% of one core, 4.0h CPU"
	if got != want {
		t.Errorf("formatMachineLoad(...) =\n%q\nwant\n%q", got, want)
	}
}

// TestFormatMachineLoad_NoForeignProcessesStillRendersBox is the clean-box
// case: a box under 600s but with nothing foreign running must still say so,
// not print a bare "box: ..." line with a trailing newline into nothing or
// an empty foreign section that looks like a bug.
func TestFormatMachineLoad_NoForeignProcessesStillRendersBox(t *testing.T) {
	got := formatMachineLoad(8, 12, nil)
	want := "box: 8 cores, load 12%"
	if got != want {
		t.Errorf("formatMachineLoad(8, 12, nil) = %q, want %q", got, want)
	}
}

// TestTopByLoad_SortsDescendingAndCaps proves the "top few" selection: the
// highest current-CPU process leads, and the list never exceeds n even when
// more foreign processes were sampled.
func TestTopByLoad_SortsDescendingAndCaps(t *testing.T) {
	in := []procSample{
		{pid: 1, pctOneCore: 10},
		{pid: 2, pctOneCore: 90},
		{pid: 3, pctOneCore: 50},
		{pid: 4, pctOneCore: 99},
	}
	got := topByLoad(in, 2)
	if len(got) != 2 {
		t.Fatalf("topByLoad(in, 2) returned %d entries, want 2", len(got))
	}
	if got[0].pid != 4 || got[1].pid != 2 {
		t.Errorf("topByLoad(in, 2) = %+v, want pid 4 then pid 2 (descending by pctOneCore)", got)
	}
}

// TestForeignLoadReport_UnavailableWhenProbeFails proves the degrade path
// #526 requires: a probe that cannot answer must render "load unavailable"
// rather than a zeroed-out, misleadingly confident report.
func TestForeignLoadReport_UnavailableWhenProbeFails(t *testing.T) {
	prev := machineLoadSampleFn
	machineLoadSampleFn = func() (int, float64, []procSample, bool) { return 0, 0, nil, false }
	t.Cleanup(func() { machineLoadSampleFn = prev })

	got := foreignLoadReport(1234)
	if !strings.Contains(got, "load unavailable") {
		t.Errorf("foreignLoadReport(...) = %q, want it to say load unavailable when the probe fails", got)
	}
}

// TestForeignLoadReport_BoundedAgainstAHungProbe is the sampling-hangs case
// #526 calls out explicitly: a probe that never returns must not hang the
// rejection it is trying to make more useful. foreignLoadReport owns its own
// bound internally (foreignLoadBudget) — calling it directly here and
// relying on the suite's own run budget to catch a regression keeps this
// test itself free of a real-time wait.
func TestForeignLoadReport_BoundedAgainstAHungProbe(t *testing.T) {
	prev := machineLoadSampleFn
	machineLoadSampleFn = func() (int, float64, []procSample, bool) {
		select {} // never returns
	}
	t.Cleanup(func() { machineLoadSampleFn = prev })

	got := foreignLoadReport(1234)
	if !strings.Contains(got, "load unavailable") {
		t.Errorf("foreignLoadReport(...) = %q, want load unavailable once the sampling budget expires", got)
	}
}
