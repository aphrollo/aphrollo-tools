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
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aphrollo-tdd-pkgtest-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude")); err != nil {
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
