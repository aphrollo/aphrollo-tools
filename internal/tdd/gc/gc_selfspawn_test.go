package gc

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// The runaway of #997: a session-start test reached the real detached sweep,
// which started the running TEST binary as `<pkg>.test gate gc ...`; a test
// binary answers that by running its whole suite, and the suite's session-start
// tests start the sweep again. Nothing here starts a process: both seams are
// replaced.
func observeSweep(t *testing.T, exe string) (started []*exec.Cmd) {
	t.Helper()
	prevExe, prevStart := gcExeFn, gcStartFn
	gcExeFn = func() (string, error) { return exe, nil }
	gcStartFn = func(cmd *exec.Cmd) { started = append(started, cmd) }
	t.Cleanup(func() { gcExeFn, gcStartFn = prevExe, prevStart })
	spawnBackgroundGC(t.TempDir())
	return started
}

func TestBackgroundGCCommand_RefusesAGoTestBinary(t *testing.T) {
	if started := observeSweep(t, "/tmp/go-build1/b001/tdd.test"); len(started) != 0 {
		t.Fatalf("the sweep started %v from a Go test binary", started[0].Args)
	}
}

func TestBackgroundGCCommand_StartsTheSweepOneGenerationDeeper(t *testing.T) {
	t.Setenv(proc.SpawnDepthEnv, "1")
	started := observeSweep(t, "/usr/local/bin/aphrollo")
	if len(started) != 1 {
		t.Fatalf("started %d sweeps, want 1", len(started))
	}
	cmd := started[0]
	// --known: a session opened outside any repo still sweeps the repos the
	// gate has worked in (issue #1005).
	if got, want := strings.Join(cmd.Args[:6], " "), "/usr/local/bin/aphrollo "+CmdName+" gc --quiet --known --repo"; got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if len(cmd.Args) != 7 {
		t.Fatalf("argv = %q, want exactly one repo argument after --repo", cmd.Args)
	}
	if cmd.Dir != cmd.Args[6] {
		t.Fatalf("Dir = %q, want the repo %q", cmd.Dir, cmd.Args[6])
	}
	var depth []string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, proc.SpawnDepthEnv+"=") {
			depth = append(depth, kv)
		}
	}
	if len(depth) != 1 || depth[0] != proc.SpawnDepthEnv+"=2" {
		t.Fatalf("child depth entries = %v, want exactly [%s=2]", depth, proc.SpawnDepthEnv)
	}
}

func TestBackgroundGCCommand_RefusesAChainAlreadyAtTheDepthCap(t *testing.T) {
	t.Setenv(proc.SpawnDepthEnv, "2")
	if started := observeSweep(t, "/usr/local/bin/aphrollo"); len(started) != 0 {
		t.Fatalf("a third generation of sweep started: %v", started[0].Args)
	}
}
