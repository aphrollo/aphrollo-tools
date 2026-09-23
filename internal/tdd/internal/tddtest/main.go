// Package tddtest holds the test helpers every internal/tdd package shares,
// and Main, the one TestMain body all of them run. It imports nothing from
// internal/tdd: what a helper needs from the package under test arrives as a
// parameter, or as a Seams field handed to Main.
package tddtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
)

// Seams is what Main needs from the package whose tests it runs.
type Seams struct {
	// Run runs the suite. TestMain passes a closure over m.Run so the call
	// stays in its own text, where the test_main_exit law looks for it; nil
	// falls back to m.Run.
	Run func() int
	// GitBinary is the git the package's own subprocesses exec. The fixture
	// helpers use the same one, so a queue shim on PATH never judges a
	// fixture repo.
	GitBinary func() string
	// HooksDirUnsafeEnv is the variable that lets a test point
	// core.hooksPath at a temp dir; IsolateGitConfig sets it.
	HooksDirUnsafeEnv string
	// BuildLockHeldEnv is unset for the run: a deferred phase tells its child
	// the lock is held, which is a fact about the phase, not about any case
	// under test.
	BuildLockHeldEnv string
	// GitQueuedEnv is set for the run, so the fixtures' bare git calls skip
	// the box's git queue.
	GitQueuedEnv string
	// SetLockDir points the package's build locks at dir and returns the undo.
	SetLockDir func(dir string) (restore func())
	// LockDirName is the package variable that resolves the machine-wide
	// lock dir; GuardLiveLockDir replaces it for the run.
	LockDirName *func() string
	// SetCIRunnerJobs installs the busy-runner probe and returns the undo.
	SetCIRunnerJobs func(fn func() []int) (restore func())
}

// active is the Seams the running Main was handed.
var active Seams

// FakeGitCommonDir is what this binary prints on STDOUT when it is standing in
// as `git` (see Main). A fixed sentinel, so the test asserting on the hooks
// path built from it needs nothing from the real git.
const FakeGitCommonDir = "/tmp/aphrollo-fake-common/.git"

// realCargoHome is what CARGO_HOME held before Main pointed it at a temp
// directory, and whether it was set at all. Only UseRealCargoHome reads them:
// every other test wants the isolated one.
var (
	realCargoHome    string
	hadRealCargoHome bool
)

// Main isolates a whole package's test run from the operator's real world,
// runs it, and returns the exit code for os.Exit.
//
// CLAUDE_CONFIG_DIR is pointed at a fresh, package-lifetime temp dir before
// any test runs. Found in review 2026-08-15: several tests never called
// t.Setenv("CLAUDE_CONFIG_DIR", ...) themselves, so loadSession/appendGateLog/
// mechCacheAdd etc. fell through to stateDir()'s real default and wrote
// directly into the operator's actual gate.log/mech-cache.json/session files
// (confirmed: gate.log picked up 562 lines from a single test run, e.g.
// "TestPrecommit_Zig_..." entries, mixed in with genuine borld/aphrollo-tools
// gate activity).
//
// A test that still calls its own t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
// is unaffected — t.Setenv overrides this default for that test's duration
// and restores it via its own registered cleanup — so per-test isolation
// keeps working exactly as before; this is purely the fallback net for the
// subset that had none.
func Main(m *testing.M, s Seams) int {
	// A test that needs a git which WRITES TO STDERR points APHROLLO_REAL_GIT
	// at this binary; git's own argv is what arrives here, so the branch is
	// taken on the verb and must be answered before testing parses flags it
	// would reject. A git that warns is the only way to tell a caller reading
	// git's STDOUT apart from one reading stdout and stderr folded together,
	// and no git CONFIG produces a warning on `rev-parse` — every invalid
	// value is fatal instead. See TestBuildInstallPlan_IgnoresGitWarningsOnStderr.
	if len(os.Args) > 1 && os.Args[1] == "rev-parse" {
		os.Stderr.WriteString("warning: unable to access '/nowhere/.config/git/config': Not a directory\n")
		os.Stdout.WriteString(FakeGitCommonDir + "\n")
		return 0
	}
	active = s
	run := s.Run
	if run == nil {
		run = m.Run
	}
	dir, err := os.MkdirTemp("", "aphrollo-tdd-pkgtest-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude")); err != nil {
		panic(err)
	}
	// Same net for the OPERATOR's real ~/.cargo/config.toml: cargoConfigTargetDir
	// (issue #285) reads a user cargo config as part of resolveTargetDir's
	// normal path, which every cargo-runner test reaches. A dev box that sets
	// build.target-dir globally would otherwise make every such test's
	// expected `<tmp>/target` wrong, depending on whichever machine happens to
	// run the suite.
	// What the box actually had is remembered first: the ONE test that runs
	// the real toolchain (the cargo-mutants smoke test) has to put it back,
	// because the cargo the box resolves lives under it and an empty
	// cargo-home resolves to nothing at all.
	realCargoHome, hadRealCargoHome = os.LookupEnv("CARGO_HOME")
	if err := os.Setenv("CARGO_HOME", filepath.Join(dir, "cargo-home")); err != nil {
		panic(err)
	}
	// Same net for the LOCK files: a test that reaches runCargoLocked without
	// setting its own override used to write target locks, slot files and
	// owner records into the operator's real %TEMP% (871 of them, measured).
	locks := filepath.Join(dir, "locks")
	if err := os.MkdirAll(locks, 0o755); err != nil {
		panic(err)
	}
	// Same reason as internal/cli's TestMain: a deferred phase tells its
	// child the lock is held, which is a fact about the phase, not about any
	// case under test here.
	os.Unsetenv(s.BuildLockHeldEnv)
	restoreLocks := s.SetLockDir(locks)
	// And the machine-wide lock dir itself is never this suite's: a test
	// that resolves it fails the package run (see GuardLiveLockDir).
	checkLiveLockDir := GuardLiveLockDir(s.LockDirName, filepath.Join(dir, "live-lock-dir-decoy"))
	// Same net for gh. Three issues were filed against the real repository by
	// nobody — #155, #196 and #197, all carrying the tdd package's own override
	// fixture values (`override:override-off r`, evidence `e`, an unfilled
	// closes-by line), two of them four seconds apart while mutation jobs
	// were starting. The tests that reach the filing path stub gh themselves
	// and every guard in front of it holds on unmutated code; under mutation
	// those guards are exactly what gets inverted, and the real gh is one
	// PATH lookup away. Putting the stub in front of it for the whole package
	// makes that unreachable rather than merely unlikely.
	if stub, err := GhStubDir(); err == nil {
		if err := os.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			panic(err)
		}
	}
	// The box's git QUEUE is not this suite's either. `git` on an operator
	// box resolves to the aphrollo shim, which takes a per-repo lock and
	// waits on the rest of the box before a mutating verb runs — right for a
	// session's own commit, wrong for a fixture building a throwaway repo,
	// which wants git and not the gate in front of it (measured in
	// internal/workspace: 25m and 60m dead in fixture git calls, against
	// 117s with the shim off PATH). The tdd PRODUCTION git already carries
	// the marker (cleanGitEnv); this is the same statement for the fixtures'
	// own bare exec.Command("git", ...) calls. Nothing here tests the shim's
	// queuing — those tests live in internal/cli, which leaves this variable
	// unset for exactly that reason.
	if err := os.Setenv(s.GitQueuedEnv, "1"); err != nil {
		panic(err)
	}
	// The golden git repos every fixture helper copies, built once here
	// rather than spawned per test. See fixture.go.
	buildFixtures(dir)
	// Nor is the box's CI. Every measurement waits for busy runner jobs
	// before it starts; this suite runs inside one on CI, beside sibling
	// runners that may be busy, and on an operator box beside all of them.
	// A test about that wait installs its own probe.
	restoreRunners := s.SetCIRunnerJobs(func() []int { return nil })
	code := run()
	if err := checkLiveLockDir(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			code = 1
		}
	}
	restoreRunners()
	restoreLocks()
	os.RemoveAll(dir)
	return code
}

// UseRealCargoHome puts the box's own CARGO_HOME back for one test. The whole
// package runs under an isolated cargo home so no test reads the operator's
// ~/.cargo/config.toml, but the cargo the queue shim resolves lives under
// CARGO_HOME: with the isolated one in the environment every `cargo` this
// test spawns fails with "resolve cargo: <tmp>\bin\cargo.exe not found", and
// the test would skip on a box that has the whole toolchain installed.
func UseRealCargoHome(t *testing.T) {
	t.Helper()
	if hadRealCargoHome {
		t.Setenv("CARGO_HOME", realCargoHome)
		return
	}
	isolated := os.Getenv("CARGO_HOME")
	if err := os.Unsetenv("CARGO_HOME"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("CARGO_HOME", isolated) })
}

// GuardLiveLockDir makes reaching the machine-wide lock dir a failure of the
// whole package run. That dir is live on every box this suite runs on: the CI
// runner and other sessions hold real locks in it while the suite runs. A test
// that resolves it can take one of those locks for an instant, sweep its
// owner records, or leave files behind, and nothing in the test's own result
// says so.
//
// It does not compare the directory's entries before and after the run: other
// processes change that directory all the time, so such a check fails on a
// busy box for reasons that are not this suite's. It intercepts the one place
// the live dir is resolved instead (the variable resolver points at), so a
// test that reaches it gets a decoy directory under the package's temp dir
// and the run is reported red with the stack that got there.
func GuardLiveLockDir(resolver *func() string, decoy string) (check func() error) {
	var reached atomic.Int64
	var once sync.Once
	var firstStack string
	prev := *resolver
	*resolver = func() string {
		reached.Add(1)
		once.Do(func() { firstStack = string(debug.Stack()) })
		return decoy
	}
	return func() error {
		*resolver = prev
		if n := reached.Load(); n > 0 {
			return fmt.Errorf("live lock dir guard: tests resolved the machine-wide lock dir %d time(s); every test must use a temp lock dir (SetLockDirForTest). First caller:\n%s", n, firstStack)
		}
		return nil
	}
}

// gitBin is the git the fixtures exec, from the running Main's seams.
func gitBin() string {
	if active.GitBinary == nil {
		panic("tddtest: no GitBinary seam; the package's TestMain must call tddtest.Main")
	}
	return active.GitBinary()
}
