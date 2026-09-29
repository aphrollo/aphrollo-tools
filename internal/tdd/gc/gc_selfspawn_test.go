package gc

import (
	"errors"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// The runaway of #997: a session-start test reached the real detached sweep,
// which started the running TEST binary as `<pkg>.test gate gc ...`; a test
// binary answers that by running its whole suite, and the suite's session-start
// tests start the sweep again.
func TestBackgroundGCCommand_RefusesAGoTestBinary(t *testing.T) {
	cmd, err := backgroundGCCommand("/tmp/go-build1/b001/tdd.test", nil, t.TempDir())
	if !errors.Is(err, proc.ErrTestBinary) {
		t.Fatalf("backgroundGCCommand(test binary) = %v, %v; want ErrTestBinary and no command", cmd, err)
	}
	if cmd != nil {
		t.Fatalf("a refused sweep still built a command: %v", cmd.Args)
	}
}

func TestBackgroundGCCommand_RefusesAChainAlreadyAtTheDepthCap(t *testing.T) {
	_, err := backgroundGCCommand("/usr/local/bin/aphrollo", []string{proc.SpawnDepthEnv + "=2"}, t.TempDir())
	if !errors.Is(err, proc.ErrSpawnDepth) {
		t.Fatalf("backgroundGCCommand at depth 2 = %v, want ErrSpawnDepth", err)
	}
}

func TestBackgroundGCCommand_StartsTheSweepOneGenerationDeeper(t *testing.T) {
	cwd := t.TempDir()
	cmd, err := backgroundGCCommand("/usr/local/bin/aphrollo", []string{proc.SpawnDepthEnv + "=1"}, cwd)
	if err != nil {
		t.Fatalf("a real binary was refused: %v", err)
	}
	if got, want := strings.Join(cmd.Args, " "), "/usr/local/bin/aphrollo "+CmdName+" gc --apply --quiet --repo "+cwd; got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if cmd.Dir != cwd {
		t.Fatalf("Dir = %q, want %q", cmd.Dir, cwd)
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
