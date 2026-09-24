package postedit

import (
	"testing"
)

// TestKillDeferred_EndsTheWholeTree pins the leak: killing only the runphase
// WRAPPER left its cargo/rustc children compiling while the wrapper's death
// released the target lock and the global slot — an unslotted orphan build,
// and a second build admitted into the same target dir. The kill must go
// through the tree killer, never a bare pid; that killer's own plan is
// pinned where it lives, in internal/proc.
func TestKillDeferred_EndsTheWholeTree(t *testing.T) {
	var got int
	prev := killTreeFn
	killTreeFn = func(pid int) error { got = pid; return nil }
	t.Cleanup(func() { killTreeFn = prev })

	killDeferred(DeferredJob{PID: 4242})
	if got != 4242 {
		t.Fatalf("killDeferred targeted pid %d, want 4242 through the tree killer", got)
	}
}
