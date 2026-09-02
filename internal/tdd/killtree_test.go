package tdd

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestKillDeferred_EndsTheWholeTree pins the leak: killing only the runphase
// WRAPPER left its cargo/rustc children compiling while the wrapper's death
// released the target lock and the global slot — an unslotted orphan build,
// and a second build admitted into the same target dir. The kill must reach
// the process group, not one pid.
func TestKillDeferred_EndsTheWholeTree(t *testing.T) {
	plan := killTreePlan(4242)
	joined := strings.Join(plan, " ")
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(joined, "/T") {
			t.Fatalf("kill plan %q does not walk the tree (/T)", joined)
		}
	default:
		if !strings.Contains(joined, "-"+strconv.Itoa(4242)) {
			t.Fatalf("kill plan %q does not name the process GROUP (-pid)", joined)
		}
	}
	if !strings.Contains(joined, "4242") {
		t.Fatalf("kill plan %q does not name the pid", joined)
	}

	var got int
	prev := killTreeFn
	killTreeFn = func(pid int) error { got = pid; return nil }
	t.Cleanup(func() { killTreeFn = prev })

	killDeferred(DeferredJob{PID: 4242})
	if got != 4242 {
		t.Fatalf("killDeferred targeted pid %d, want 4242 through the tree killer", got)
	}
}
