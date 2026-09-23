package suite

import "testing"

// Fixtures here follow vitest's documented default-reporter summary shape —
// see vacuous_vitest.go's own comment for why this file, unlike its cargo
// and pytest siblings, has no real-toolchain capture behind it.

// TestVitestVacuous_EmptyWhenRealTestsRan is the base case.
func TestVitestVacuous_EmptyWhenRealTestsRan(t *testing.T) {
	t.Parallel()
	const output = " Test Files  1 passed (1)\n      Tests  3 passed (3)\n   Start at  12:00:00\n   Duration  123ms\n"
	if vitestVacuous(output) {
		t.Fatal("vitestVacuous = true, want false — three tests genuinely passed")
	}
}

// TestVitestVacuous_SkippedAloneIsNotVacuous: every collected test skipped
// still counts toward the parenthesized grand total — skip is not the same
// as not executed (#412).
func TestVitestVacuous_SkippedAloneIsNotVacuous(t *testing.T) {
	t.Parallel()
	const output = " Test Files  1 passed (1)\n      Tests  2 skipped (2)\n"
	if vitestVacuous(output) {
		t.Fatal("vitestVacuous = true, want false — a skip decision still counts toward the grand total")
	}
}

// TestVitestVacuous_ZeroGrandTotal: a related file loaded without error but
// contained zero test cases — the #194-shaped defect this parser exists to
// catch.
func TestVitestVacuous_ZeroGrandTotal(t *testing.T) {
	t.Parallel()
	const output = " Test Files  1 passed (1)\n      Tests  no tests (0)\n"
	if !vitestVacuous(output) {
		t.Fatal("vitestVacuous = false, want true — the Tests line's own grand total is zero")
	}
}

// TestVitestVacuous_EmptyOnATransformFailure is the "must not relabel a
// build failure as vacuous" fixture #412 asks for: a syntax/transform error
// never reaches a "Tests" summary line at all.
func TestVitestVacuous_EmptyOnATransformFailure(t *testing.T) {
	t.Parallel()
	const transformFailure = "FAIL  src/widget.test.ts [ src/widget.test.ts ]\n" +
		"Transform failed with 1 error:\n" +
		"src/widget.test.ts:3:1: ERROR: Unexpected token\n"
	if vitestVacuous(transformFailure) {
		t.Fatal("vitestVacuous = true, want false — a transform failure prints no Tests summary line to misread")
	}
}
