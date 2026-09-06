package tdd

import (
	"regexp"
	"strconv"
)

// #412 (vitest half): the counterpart to vacuousGoPackages (classify.go) for
// vitest's default reporter, whose final summary carries a "Tests" line with
// a parenthesized grand total, e.g.:
//
//	Test Files  1 passed (1)
//	     Tests  3 passed (3)
//
// or, with failures/skips broken out before the total:
//
//	Tests  1 failed | 2 passed (3)
//	Tests  1 skipped | 2 passed (3)
//
// Unlike cargoVacuousTargets and pytestVacuous, this has no ground truth
// measured against a real install on this box (no vitest project was
// available to run) — the shape above is the documented default-reporter
// format, not a captured one. Rather than invent an unverified
// filtered/excluded discriminator on top of an unverified base shape, this
// reads only the one number the format reliably carries either way: the
// parenthesized grand total on the "Tests" line. A run whose narrowed scope
// (narrowToStaged's `vitest related <files> --run`) genuinely finds no
// related tests exits NONZERO by default ("No test files found") and never
// reaches this check at all (res.Passed gates every vacuous check the same
// way for every runner) — so the remaining case this catches is a file that
// DID load, without error, and simply contained zero test cases.
var vitestTestsSummaryRe = regexp.MustCompile(`(?m)^\s*Tests\s+.*?\((\d+)\)\s*$`)

// vitestVacuous reports whether output's "Tests" summary line carries a
// grand total of exactly zero.
func vitestVacuous(output string) bool {
	m := vitestTestsSummaryRe.FindStringSubmatch(output)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n == 0
}
