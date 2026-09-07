package tdd

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// TestRunSuite_OrphanHeldPipeAfterCleanExit pins the WaitDelay contract: a
// suite whose process EXITS 0 but leaves an orphaned child holding the output
// pipe past WaitDelay must be reported Passed. Go's exec returns ErrWaitDelay
// INSTEAD of nil in exactly that case, and treating any non-nil error as RED
// turned a green commit into a phantom "tests failing" block (observed on
// Windows, where lingering build/tool children — mspdbsrv and friends —
// inherit the pipe and outlive cargo).
func TestRunSuite_OrphanHeldPipeAfterCleanExit(t *testing.T) {
	t.Parallel()
	var r Runner
	if runtime.GOOS == "windows" {
		// `start /b` spawns ping sharing cmd's std handles; cmd itself exits 0
		// immediately, ping holds the pipe ~5s — past the 2s WaitDelay.
		r = Runner{Cmd: "cmd", Args: []string{"/c", "start /b ping -n 6 127.0.0.1 & exit 0"}}
	} else {
		r = Runner{Cmd: "sh", Args: []string{"-c", "sleep 5 & exit 0"}}
	}
	// os.TempDir, not t.TempDir: the orphan outlives the test body and must
	// not hold a cleanup-checked directory open on Windows.
	res := RunSuite(20*time.Second)(r, os.TempDir())
	if res.TimedOut {
		t.Fatal("a clean exit within the deadline must not be TimedOut")
	}
	if !res.Passed {
		t.Fatal("a process that exits 0 must be Passed even when an orphaned child holds the output pipe past WaitDelay")
	}
}
