//go:build windows

package postedit

import (
	"os"
	"os/exec"
	"testing"
)

// pidRunning used to answer by shelling out to `tasklist`. Every one of those
// is a console program, so every liveness check allocated a console — and a
// caller that polls (a stranded test binary did exactly this) turned that into
// a window opening several times a second, indefinitely (issue #204). The
// answer has to come from the OS directly, with no child process at all.

// TestPidRunning_ReportsThisProcessAlive is the true case: this test's own
// process is unambiguously running.
func TestPidRunning_ReportsThisProcessAlive(t *testing.T) {
	if !pidRunning(os.Getpid()) {
		t.Error("pidRunning(os.Getpid()) = false, want true — this process is running")
	}
}

// TestPidRunning_ReportsAnExitedProcessNotRunning is the false case, and the
// one that matters: a job whose process has exited must be reported finished,
// or the runner waits on it forever.
func TestPidRunning_ReportsAnExitedProcessNotRunning(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit", "0")
	cmd.SysProcAttr = detachedAttrs()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if pidRunning(pid) {
		t.Errorf("pidRunning(%d) = true after the process exited and was reaped, want false", pid)
	}
}

// TestPidRunning_AnswersWithoutRunningAnExternalCommand is the one that goes
// red against the tasklist implementation, and the reason this rewrite
// exists. With PATH emptied there is no tasklist to find, so the old code
// took its "cannot tell" branch and called a reaped pid alive; an answer that
// comes from the OS does not depend on finding a program at all — which is
// exactly why it no longer opens a console to get it.
func TestPidRunning_AnswersWithoutRunningAnExternalCommand(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit", "0")
	cmd.SysProcAttr = detachedAttrs()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	t.Setenv("PATH", "")
	if pidRunning(pid) {
		t.Errorf("pidRunning(%d) = true with PATH emptied, want false — the answer must not come from an external command", pid)
	}
}

// TestPidRunning_RejectsANonPositivePid pins the guard: 0 and negative are not
// pids, and must never be reported as a live process.
func TestPidRunning_RejectsANonPositivePid(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if pidRunning(pid) {
			t.Errorf("pidRunning(%d) = true, want false", pid)
		}
	}
}
