//go:build !windows

package lock

import "testing"

// TestMachineLoadSample_UnimplementedOutsideWindowsReportsUnavailable pins
// the non-Windows degrade: issue #526 only has a Windows reader
// (tasklist/GetProcessTimes), so every other platform must answer exactly
// the same "load unavailable" shape (ok=false, zero cores and load, no
// samples) a reader here would already treat as "not answered", never a
// guessed reading.
func TestMachineLoadSample_UnimplementedOutsideWindowsReportsUnavailable(t *testing.T) {
	cores, loadPct, procs, ok := machineLoadSample(nil)
	if ok {
		t.Fatal("ok = true, want false — non-Windows has no sampler yet")
	}
	if cores != 0 || loadPct != 0 || procs != nil {
		t.Fatalf("got cores=%d loadPct=%v procs=%v, want the zero values ok=false pairs with", cores, loadPct, procs)
	}
}
