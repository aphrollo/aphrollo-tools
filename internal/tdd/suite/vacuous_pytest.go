package suite

import (
	"regexp"
	"strconv"
)

// #412 (pytest half): the counterpart to vacuousGoPackages (classify.go) for
// pytest's own summary line. This gate runs `pytest -q` (runner.go's
// DetectRunner), whose quiet summary carries no "collected N items" header —
// only the final line's own categories, e.g. (all measured against the real
// tool, not guessed):
//
//	2 passed, 1 skipped in 0.01s          (real tests ran; skipped ≠ vacuous)
//	1 skipped, 2 deselected in 0.00s      (a -k/-m filter excluded SOME, one still ran)
//	3 deselected in 0.00s                 (a -k/-m filter excluded EVERY collected test)
//	no tests ran in 0.00s                 (nothing was ever collected)
//
// pytest itself already exits 5 ("no tests collected") for the last two
// shapes, which case #411/#412 exists for expects to reach this check only
// when something ELSE (a plugin, an ini `addopts` override) has laundered
// that exit code back to 0 — the same defense-in-depth reason cargo's
// filtered_out check exists even though a bare filter mismatch there also
// already reads as a genuine failure under this gate's own invocations.
//
// deselected is pytest's OWN count of tests that existed but were excluded
// by the selection (-k/-m, or a test-id filter) — its exact counterpart to
// cargo's filtered_out. Every OTHER category (passed, failed, error,
// skipped, xfailed, xpassed) means pytest's engine decided something about a
// real, collected test, so all of them count as "executed" — skipped is not
// the same as not executed (#412's own stated constraint, and this gate's
// existing collected-vs-executed pytest note).
var pytestSummaryCategoryRe = regexp.MustCompile(
	`(\d+) (passed|failed|error(?:s)?|skipped|xfailed|xpassed|deselected)`)

// pytestVacuous reports whether output's final summary shows zero executed
// (passed+failed+error+skipped+xfailed+xpassed) while deselected is nonzero:
// real, collected tests existed and every one of them was excluded by the
// gate's own selection, rather than the target simply having none. A summary
// with no deselected mention (including "no tests ran", which carries no
// category tokens at all) means nothing to distinguish "excluded" from
// "never existed", so it is left alone — the safe direction in cost, exactly
// as cargoVacuousTargets treats an unreadable dependency graph.
func pytestVacuous(output string) bool {
	executed, deselected := 0, 0
	for _, m := range pytestSummaryCategoryRe.FindAllStringSubmatch(output, -1) {
		n, _ := strconv.Atoi(m[1])
		if m[2] == "deselected" {
			deselected += n
		} else {
			executed += n
		}
	}
	return executed == 0 && deselected > 0
}
