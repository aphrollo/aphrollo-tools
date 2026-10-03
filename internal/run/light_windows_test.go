//go:build windows

package run

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

// A light child that exits on its own leaves what it started running, as a
// light child's own exit has always done: the job that held it lets go
// instead of ending the tree the way a heavy child's does.
func TestLightRun_AnExitedChildLeavesItsGrandchildRunning(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	spec := chains()[0].spec(t, pidFile, true)
	spec.Timeout = time.Minute

	if err := LightRun(spec); err != nil {
		t.Fatalf("LightRun = %v: the child was meant to exit cleanly", err)
	}

	pids := readPids(pidFile)
	watch(t, pids)
	if len(pids) < 2 {
		t.Fatalf("the tree recorded %d pids, want 2", len(pids))
	}
	grandchild := pids[1]
	if runtest.AllGone([]int{grandchild}, 2*time.Second) {
		t.Errorf("grandchild %d was ended when the light child exited, want it left running", grandchild)
	}
}
