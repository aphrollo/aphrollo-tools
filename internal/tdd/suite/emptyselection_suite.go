package suite

import (
	"regexp"
	"strconv"
)

// NoTestsSelected is the verdict for a run that executed ZERO tests.
// Inconclusive family, alongside TIMEOUT/SKIPPED/QUEUED-SKIPPED,
// DeferredAbandoned and InfraFailed — the code was NOT tested — and
// deliberately NOT a shape isSettledVerdict recognises: a run that tested
// nothing must not silence the hand-run that would have caught it.
const NoTestsSelected = "no-tests-selected"

// cargoNarrowingValueFlags and cargoNarrowingBareFlags are the narrowings a
// post-edit run adds WITHIN a package — the target selection and the name
// filter, in both dialects. Widening drops exactly these and keeps
// everything else, so the package scope (`-p <pkg>`), the workspace
// directory and any behaviour flag survive untouched.
var (
	cargoNarrowingValueFlags = map[string]bool{"--test": true, "--bin": true, "-E": true, "--filter-expr": true}
	cargoNarrowingBareFlags  = map[string]bool{"--lib": true, "--bins": true, "--doc": true}
	cargoScopeValueFlags     = map[string]bool{"-p": true, "--package": true}
)

// libtestRanNoTests reports whether EVERY libtest result block in the output
// ran nothing. A run with no result block at all (a compile error, a spawn
// failure) is not a zero selection — it is a failure, and reading it as one
// is what keeps a broken build out of this path.
func libtestRanNoTests(output string) bool {
	blocks := cargoTestResultRe.FindAllStringSubmatch(output, -1)
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		for _, n := range b[1:4] { // passed, failed, ignored
			if v, err := strconv.Atoi(n); err != nil || v != 0 {
				return false
			}
		}
	}
	return true
}

// nextestSummaryRe reads the test count off cargo-nextest's own summary
// line, the one shape that reports a zero selection while still exiting 0.
// nextest's other zero shape — exit 4 with "no tests to run" — is
// noTestsToRunRe's, already recognised for treatAsEmptyPass.
var nextestSummaryRe = regexp.MustCompile(`(?m)^\s*Summary\s*\[[^\]]*\]\s*(\d+)\s*tests?\s*run:`)

// selectedZeroTests reports whether a cargo run executed no test at all, in
// either dialect: nextest's exit-4 "no tests to run" and its zero summary,
// or libtest's own per-target result blocks all reporting zero passed, zero
// failed and zero ignored (an #[ignore]d test still counts as executed —
// libtest itself decided to skip it, see vacuous_cargo.go). A timed-out run
// is never read this way: it says nothing about selection, only about the
// clock.
func selectedZeroTests(r Runner, res SuiteResult) bool {
	if r.Cmd != "cargo" || res.TimedOut {
		return false
	}
	if noTestsToRunRe.MatchString(res.Output) {
		return true
	}
	if m := nextestSummaryRe.FindStringSubmatch(res.Output); m != nil {
		return m[1] == "0"
	}
	return libtestRanNoTests(res.Output)
}
