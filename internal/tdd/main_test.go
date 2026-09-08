package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates the WHOLE package's test run from the operator's REAL
// ~/.claude/gate-state: CLAUDE_CONFIG_DIR is pointed at a fresh,
// package-lifetime temp dir before any test runs. Found in review
// 2026-08-15: several tests never called t.Setenv("CLAUDE_CONFIG_DIR", ...)
// themselves, so loadSession/appendGateLog/mechCacheAdd etc. fell through to
// stateDir()'s real default and wrote directly into the operator's actual
// gate.log/mech-cache.json/session files (confirmed: gate.log picked up 562
// lines from a single test run, e.g. "TestPrecommit_Zig_..." entries, mixed
// in with genuine borld/aphrollo-tools gate activity).
//
// A test that still calls its own t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
// is unaffected — t.Setenv overrides this default for that test's duration
// and restores it via its own registered cleanup — so per-test isolation
// keeps working exactly as before; this is purely the fallback net for the
// subset that had none.
// fakeGitCommonDir is what this binary prints on STDOUT when it is standing in
// as `git` (see below). A fixed sentinel, so the test asserting on the hooks
// path built from it needs nothing from the real git.
const fakeGitCommonDir = "/tmp/aphrollo-fake-common/.git"

func TestMain(m *testing.M) {
	// A test that needs a git which WRITES TO STDERR points APHROLLO_REAL_GIT
	// at this binary; git's own argv is what arrives here, so the branch is
	// taken on the verb and must be answered before testing parses flags it
	// would reject. A git that warns is the only way to tell a caller reading
	// git's STDOUT apart from one reading stdout and stderr folded together,
	// and no git CONFIG produces a warning on `rev-parse` — every invalid
	// value is fatal instead. See TestBuildInstallPlan_IgnoresGitWarningsOnStderr.
	if len(os.Args) > 1 && os.Args[1] == "rev-parse" {
		os.Stderr.WriteString("warning: unable to access '/nowhere/.config/git/config': Not a directory\n")
		os.Stdout.WriteString(fakeGitCommonDir + "\n")
		os.Exit(0)
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
	// normal path, which every cargo-runner test in this package reaches. A
	// dev box that sets build.target-dir globally would otherwise make every
	// such test's expected `<tmp>/target` wrong, depending on whichever
	// machine happens to run the suite.
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
	os.Unsetenv(BuildLockHeldEnv)
	restoreLocks := SetLockDirForTest(locks)
	// Same net for gh. Three issues were filed against the real repository by
	// nobody — #155, #196 and #197, all carrying this package's own override
	// fixture values (`override:override-off r`, evidence `e`, an unfilled
	// closes-by line), two of them four seconds apart while mutation jobs
	// were starting. The tests that reach the filing path stub gh themselves
	// and every guard in front of it holds on unmutated code; under mutation
	// those guards are exactly what gets inverted, and the real gh is one
	// PATH lookup away. Putting the stub in front of it for the whole package
	// makes that unreachable rather than merely unlikely.
	if stub, err := ghStubDir(); err == nil {
		if err := os.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			panic(err)
		}
	}
	// The golden git repos every fixture helper copies, built once here
	// rather than spawned per test. See fixture_test.go.
	buildFixtures(dir)
	code := m.Run()
	restoreLocks()
	os.RemoveAll(dir)
	os.Exit(code)
}
