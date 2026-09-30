package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// realHomeAtStart is the operator's home as it stood before TestMain
// redirected it, so the isolation guard can name what must stay untouched.
// Empty when the box has no resolvable home.
var realHomeAtStart string

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
	if msg, refuse := proc.RefuseTestReexec(os.Args); refuse {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(2)
	}
	dir, err := os.MkdirTemp("", "aphrollo-cli-pkgtest-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude")); err != nil {
		panic(err)
	}
	realHomeAtStart, _ = os.UserHomeDir()
	gitiso.MustIsolate(dir)
	// os.Executable is this test binary. Anything init wires to "the running
	// binary" would name a Go test binary, which answers gate arguments by
	// running its whole suite (#997); the writers refuse it, so tests that
	// leave --bin off get a stand-in gate path.
	standIn := filepath.Join(dir, "bin", "aphrollo"+exeSuffix())
	if err := os.MkdirAll(filepath.Dir(standIn), 0o755); err != nil {
		panic(err)
	}
	if err := proc.WriteExecutable(standIn, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		panic(err)
	}
	execPathFn = func() (string, error) { return standIn, nil }
	locks := filepath.Join(dir, "locks")
	if err := os.MkdirAll(locks, 0o755); err != nil {
		panic(err)
	}
	// The gate may run this suite from inside a deferred phase, which tells
	// its child the build lock is HELD. That is true of the phase, not of a
	// test case about the shim's queuing — inherited, it made every shim
	// test take the nested-passthrough path.
	os.Unsetenv(tdd.BuildLockHeldEnv)
	// The mutation-gate marker is the same kind of fact: true of the
	// measurement that runs this suite, false of any test case in it.
	// Inherited, it sent every shim test down the marked path, and one of
	// them waited forever for a queue line that path never prints (#704).
	os.Unsetenv(tdd.MutationGateEnv)
	// And the same net for gh: this package's verbs shell out to it, and only
	// the tests that arranged a stub were isolated from the operator's real
	// one. See ghstub_isolation_test.go.
	if stub, err := ghStubDir(); err == nil {
		registerStubDir(stub)
		if err := os.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			panic(err)
		}
	}
	restoreLocks := tdd.SetLockDirForTest(locks)
	// The real smoke check spawns the candidate binary (see selfinstall.go);
	// nothing in this package's suite ever builds one — buildAphrollo is
	// stubbed everywhere it is reached — so it would refuse every fixture's
	// plain-byte "binary". Default it permissive here; the one test that
	// pins the refusal overrides it locally.
	runSmokeCheckFn = func(string) error { return nil }
	// A measurement waits for this box's busy CI runner jobs before it
	// starts; the mutants verbs measure through that wait, and neither the
	// CI job this suite may run inside nor its siblings are a fixture.
	restoreRunners := tdd.SetCIRunnerJobsForTest(func() []int { return nil })
	code := m.Run()
	restoreRunners()
	restoreLocks()
	os.RemoveAll(dir)
	stubDirsMu.Lock()
	dirs := append([]string(nil), stubDirs...)
	stubDirsMu.Unlock()
	for _, d := range dirs {
		os.RemoveAll(d)
		if _, err := os.Stat(d); err == nil {
			fmt.Fprintf(os.Stderr, "cli: registered stub dir %s survived cleanup\n", d)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}
