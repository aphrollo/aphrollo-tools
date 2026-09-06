package tdd

import "testing"

// Every fixture below is captured verbatim from a real `pytest -q` run on
// this box — the exact invocation DetectRunner uses (runner.go) — against a
// throwaway three-test module, not invented.

// TestPytestVacuous_EmptyWhenRealTestsRan: "2 passed, 1 skipped in 0.01s" —
// the base case.
func TestPytestVacuous_EmptyWhenRealTestsRan(t *testing.T) {
	if pytestVacuous("..s                                                                      [100%]\n2 passed, 1 skipped in 0.01s\n") {
		t.Fatal("pytestVacuous = true, want false — two tests genuinely passed")
	}
}

// TestPytestVacuous_SkippedAloneIsNotVacuous: every collected test skipped,
// with NO deselection involved ("2 skipped in 0.01s") — skip is not the same
// as not executed (#412's own stated constraint): pytest's engine decided
// something about two real, collected tests.
func TestPytestVacuous_SkippedAloneIsNotVacuous(t *testing.T) {
	if pytestVacuous("ss                                                                       [100%]\n2 skipped in 0.01s\n") {
		t.Fatal("pytestVacuous = true, want false — a skip decision is still an executed test")
	}
}

// TestPytestVacuous_PartialDeselectionWithARealSkipIsNotVacuous:
// "1 skipped, 2 deselected in 0.00s" — a -k filter excluded two tests, but
// one collected test still ran through pytest's engine (and was skipped).
// Not vacuous: SOMETHING executed.
func TestPytestVacuous_PartialDeselectionWithARealSkipIsNotVacuous(t *testing.T) {
	if pytestVacuous("s                                                                        [100%]\n1 skipped, 2 deselected in 0.00s\n") {
		t.Fatal("pytestVacuous = true, want false — one collected test still ran")
	}
}

// TestPytestVacuous_EveryCollectedTestDeselected: "3 deselected in 0.00s" —
// a -k/-m filter excluded every one of the three tests pytest collected.
// This is vacuousFailFirstMessage's own "name/filter mismatch" shape:
// real tests existed and every one of them was excluded.
func TestPytestVacuous_EveryCollectedTestDeselected(t *testing.T) {
	if !pytestVacuous("\n3 deselected in 0.00s\n") {
		t.Fatal("pytestVacuous = false, want true — three real tests were collected and every one excluded")
	}
}

// TestPytestVacuous_NoTestsRanIsNotVacuous: "no tests ran in 0.00s" — no
// test files existed at all, pytest's OWN "genuinely nothing to run" phrase,
// carrying no category tokens for this parser to read at all. Not the same
// defect as a filter mismatch: nothing existed to filter.
func TestPytestVacuous_NoTestsRanIsNotVacuous(t *testing.T) {
	if pytestVacuous("\nno tests ran in 0.00s\n") {
		t.Fatal("pytestVacuous = true, want false — nothing was ever collected, not excluded")
	}
}

// TestPytestVacuous_EmptyOnACollectionError is the "must not relabel a
// build failure as vacuous" fixture #412 asks for: a real import error's
// summary line carries no passed/failed/skipped/deselected token at all.
func TestPytestVacuous_EmptyOnACollectionError(t *testing.T) {
	const collectionError = "ImportError while importing test module 'test_widget.py'.\n" +
		"E   ModuleNotFoundError: No module named 'widget'\n" +
		"1 error in 0.05s\n"
	if pytestVacuous(collectionError) {
		t.Fatal("pytestVacuous = true, want false — a collection error is a different outcome, never relabeled vacuous")
	}
}
