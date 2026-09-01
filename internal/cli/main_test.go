package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// stubDirs holds the temp dirs the compiled shim stubs are built into. They
// are built ONCE per test binary and shared by every test that needs them,
// so a t.Cleanup on the first test would pull the stub out from under the
// rest — the package's own run is the only lifetime that matches.
var (
	stubDirsMu sync.Mutex
	stubDirs   []string
)

func registerStubDir(dir string) {
	stubDirsMu.Lock()
	defer stubDirsMu.Unlock()
	stubDirs = append(stubDirs, dir)
}

// TestMain isolates the package's run from the operator's machine: the state
// dir, and every aphrollo lock file the shims take. Without the lock-dir
// override the shim tests wrote their target locks and slot files straight
// into the real %TEMP% and left them there.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aphrollo-cli-pkgtest-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude")); err != nil {
		panic(err)
	}
	locks := filepath.Join(dir, "locks")
	if err := os.MkdirAll(locks, 0o755); err != nil {
		panic(err)
	}
	// The gate may run this suite from inside a deferred phase, which tells
	// its child the build lock is HELD. That is true of the phase, not of a
	// test case about the shim's queuing — inherited, it made every shim
	// test take the nested-passthrough path.
	os.Unsetenv(tdd.BuildLockHeldEnv)
	restoreLocks := tdd.SetLockDirForTest(locks)
	code := m.Run()
	restoreLocks()
	os.RemoveAll(dir)
	stubDirsMu.Lock()
	for _, d := range stubDirs {
		os.RemoveAll(d)
	}
	stubDirsMu.Unlock()
	os.Exit(code)
}
