//go:build linux

package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

// The parent of a heavy child dies the one way no handler sees, a SIGKILL:
// the kernel must end the child with it. The parent is this test binary in
// helper mode "parent", which starts the child through StartHeavy, writes its
// pid to a file and holds.
func TestHeavy_ChildDoesNotOutliveAParentThatIsKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	parent := exec.Command(os.Args[0], noTests)
	parent.Env = append(os.Environ(), helperEnv+"=parent")
	parent.Args = append(parent.Args, pidFile)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	watch(t, []int{parent.Process.Pid})
	var pids []int
	waitFor(t, "the parent to start its child", func() bool {
		pids = runtest.ReadPids(pidFile)
		return len(pids) == 1
	})
	watch(t, pids)
	if !alive(pids[0]) {
		t.Fatalf("child %d is not running before its parent is killed, so the test proves nothing", pids[0])
	}

	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()

	if !runtest.AllGone(pids, 2*time.Second) {
		t.Fatalf("child %d is still running 2 s after its parent was killed", pids[0])
	}
}
