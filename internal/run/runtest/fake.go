package runtest

import (
	"testing"
	"time"
)

// RequireTreeGone fails the test unless the tree recorded at least want pids in
// pidFile (it came up) and every one of them is gone within 20 seconds of the
// call: the proof the child's whole tree ended with it, not just its own
// process. A pid left running is ended when the test is over.
func RequireTreeGone(t testing.TB, pidFile string, want int) {
	t.Helper()
	pids := ReadPids(pidFile)
	t.Cleanup(func() {
		for _, pid := range pids {
			ForceKill(pid)
		}
	})
	if len(pids) < want {
		t.Fatalf("the tree recorded %d pids, want %d: it never came up, so nothing is proven", len(pids), want)
	}
	if !AllGone(pids, 20*time.Second) {
		t.Errorf("a pid of the tree %v outlived the child that was ended", pids)
	}
}
