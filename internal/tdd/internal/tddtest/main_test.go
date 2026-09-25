package tddtest

import (
	"os"
	"testing"
)

// TestMain runs tddtest's own (small) test suite through the same Main every
// consuming internal/tdd package runs its tests through — the
// test_main_exit/TestTDDPackages_EachRunsTestMainThroughTddtestMain law scans
// every package under internal/tdd for a TestMain calling tddtest.Main, this
// package included, and Main's own isolation (a fresh CLAUDE_CONFIG_DIR and
// CARGO_HOME, everything else left off since this package owns no build
// lock, lock dir, CI probe or git seam) is exactly what a test of a helper
// meant to run under that isolation should want too.
func TestMain(m *testing.M) {
	os.Exit(Main(m, Seams{Run: func() int { return m.Run() }}))
}
