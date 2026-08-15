package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates the WHOLE package's test run from the operator's REAL
// ~/.claude/tdd-state: CLAUDE_CONFIG_DIR is pointed at a fresh,
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
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
