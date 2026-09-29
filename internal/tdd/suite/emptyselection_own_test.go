package suite

import "testing"

// These are suite's own tests of emptyselection_suite.go: libtestRanNoTests
// and selectedZeroTests, reached today only through internal/tdd/postedit and
// precommit.

const (
	libtestZero   = "test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	libtestPassed = "test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
)

// TestLibtestRanNoTests_EveryBlockZeroIsNoTests pins the positive case: every
// summary block reports zero passed, failed and ignored.
func TestLibtestRanNoTests_EveryBlockZeroIsNoTests(t *testing.T) {
	t.Parallel()
	if !libtestRanNoTests(libtestZero + libtestZero) {
		t.Fatal("two all-zero summaries ran no tests")
	}
}

// TestLibtestRanNoTests_OneBlockThatRanSomethingMakesItATestRun pins that a
// single block with any passed, failed or ignored test ends the verdict, even
// when other blocks are empty and wherever it sits.
func TestLibtestRanNoTests_OneBlockThatRanSomethingMakesItATestRun(t *testing.T) {
	t.Parallel()
	failed := "test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	ignored := "test result: ok. 0 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	for name, out := range map[string]string{
		"passed first":  libtestPassed + libtestZero,
		"passed last":   libtestZero + libtestPassed,
		"failed block":  libtestZero + failed,
		"ignored block": libtestZero + ignored,
	} {
		if libtestRanNoTests(out) {
			t.Errorf("%s: libtestRanNoTests = true, want false", name)
		}
	}
}

// TestLibtestRanNoTests_NoSummaryAtAllIsNotAnEmptyRun pins that output with no
// libtest summary says nothing about an empty selection.
func TestLibtestRanNoTests_NoSummaryAtAllIsNotAnEmptyRun(t *testing.T) {
	t.Parallel()
	if libtestRanNoTests("compiling...\n") {
		t.Fatal("no summary block means no verdict")
	}
}

// TestSelectedZeroTests_OnlyCargoRunsCount pins the runner guard.
func TestSelectedZeroTests_OnlyCargoRunsCount(t *testing.T) {
	t.Parallel()
	if selectedZeroTests(Runner{Cmd: "go"}, SuiteResult{Output: libtestZero}) {
		t.Fatal("a go runner never selects zero tests by this reading")
	}
}

// TestSelectedZeroTests_ATimedOutRunProvedNothing pins that a run that hit its
// deadline is not an empty selection, whatever its partial output says.
func TestSelectedZeroTests_ATimedOutRunProvedNothing(t *testing.T) {
	t.Parallel()
	if selectedZeroTests(Runner{Cmd: "cargo"}, SuiteResult{TimedOut: true, Output: libtestZero}) {
		t.Fatal("a timed-out run must not read as an empty selection")
	}
}

// TestSelectedZeroTests_NextestNoTestsToRunIsEmpty pins nextest's hard-failure
// text.
func TestSelectedZeroTests_NextestNoTestsToRunIsEmpty(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Output: "error: no tests to run\n"}
	if !selectedZeroTests(Runner{Cmd: "cargo"}, res) {
		t.Fatal("nextest's no-tests-to-run is an empty selection")
	}
}

// TestSelectedZeroTests_ANextestSummaryIsReadByItsCount pins the summary line:
// zero run is empty, any other count is not.
func TestSelectedZeroTests_ANextestSummaryIsReadByItsCount(t *testing.T) {
	t.Parallel()
	zero := SuiteResult{Output: "     Summary [   0.001s] 0 tests run: 0 passed, 0 skipped\n"}
	if !selectedZeroTests(Runner{Cmd: "cargo"}, zero) {
		t.Fatal("a summary of 0 tests run is empty")
	}
	some := SuiteResult{Output: "     Summary [   0.104s] 12 tests run: 12 passed, 0 skipped\n"}
	if selectedZeroTests(Runner{Cmd: "cargo"}, some) {
		t.Fatal("a summary of 12 tests run is not empty")
	}
}

// TestSelectedZeroTests_AnExecutedNextestSummaryBeatsAnEmptyLibtestBlock pins
// the precedence: a nextest summary that ran tests decides the answer even if
// a libtest block above it was all zero (a doctest target, say).
func TestSelectedZeroTests_AnExecutedNextestSummaryBeatsAnEmptyLibtestBlock(t *testing.T) {
	t.Parallel()
	res := SuiteResult{Output: libtestZero + "     Summary [   0.104s] 3 tests run: 3 passed, 0 skipped\n"}
	if selectedZeroTests(Runner{Cmd: "cargo"}, res) {
		t.Fatal("the nextest summary says 3 tests ran")
	}
}

// TestSelectedZeroTests_PlainCargoTestFallsBackToTheLibtestBlocks pins the last
// arm: no nextest markers, so the libtest summaries decide.
func TestSelectedZeroTests_PlainCargoTestFallsBackToTheLibtestBlocks(t *testing.T) {
	t.Parallel()
	if !selectedZeroTests(Runner{Cmd: "cargo"}, SuiteResult{Output: libtestZero}) {
		t.Fatal("an all-zero libtest summary is an empty selection")
	}
	if selectedZeroTests(Runner{Cmd: "cargo"}, SuiteResult{Output: libtestPassed}) {
		t.Fatal("a libtest summary with passes is not")
	}
}
