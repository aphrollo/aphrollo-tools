package suite

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// waitDelayFixtureTimeout bounds this fixture's own real run, not the
// behaviour it pins. The worst-case work here is tiny (the 5s orphan sleep,
// capped by the 2s WaitDelay — ~2s in practice, confirmed locally), and this
// Runner is plain `sh`/`cmd`: neither `cargo` nor `go test -race`, so
// runCargoLocked's build-slot lock (buildlock_suite.go) never runs for it —
// this call goes straight to the bare SuiteRunner with no queue wait folded
// into its budget. A short deadline nonetheless flaked on the shared
// self-hosted CI runner (gate-env, run 35951517301): with concurrent CI jobs
// pushing load average to ~20, plain OS scheduling of this trivial fork/exec
// stretched to 83.85s, past a 20s deadline, and RunSuite reported a spurious
// TimedOut for a process that never even ran long. Widen the margin so
// contention alone cannot manufacture that false TimedOut; the assertions
// below (TimedOut false, Passed true) are unchanged.
const waitDelayFixtureTimeout = 180 * time.Second

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
	res := RunSuite(waitDelayFixtureTimeout)(r, os.TempDir())
	if res.TimedOut {
		t.Fatal("a clean exit within the deadline must not be TimedOut")
	}
	if !res.Passed {
		t.Fatal("a process that exits 0 must be Passed even when an orphaned child holds the output pipe past WaitDelay")
	}
}
