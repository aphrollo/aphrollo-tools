package postedit

import (
	"reflect"
	"testing"
	"time"
)

// Tests above postedit stub the three deferred-phase process seams only
// through these setters, so each must install its stub and restore the real
// seam.
func TestDeferredSeamSetters_InstallAndRestore(t *testing.T) {
	same := func(a, b any) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }

	restore := SetSpawnPhaseForTest(func(j DeferredJob) (DeferredJob, bool) { j.PID = 77; return j, true })
	if j, ok := spawnPhaseFn(DeferredJob{}); !ok || j.PID != 77 {
		t.Error("spawn stub not installed")
	}
	restore()
	if !same(spawnPhaseFn, spawnPhase) {
		t.Error("restore left the spawn stub in place")
	}

	killed := 0
	restore = SetKillDeferredForTest(func(DeferredJob) { killed++ })
	killDeferredFn(DeferredJob{})
	if killed != 1 {
		t.Error("kill stub not installed")
	}
	restore()
	if !same(killDeferredFn, killDeferred) {
		t.Error("restore left the kill stub in place")
	}

	at := time.Unix(1_700_000_000, 0)
	restore = SetProcessStartTimeForTest(func(int) (time.Time, bool) { return at, true })
	if got, ok := processStartTimeFn(1); !ok || !got.Equal(at) {
		t.Error("process-start stub not installed")
	}
	restore()
	if !same(processStartTimeFn, processStartTime) {
		t.Error("restore left the process-start stub in place")
	}
}
