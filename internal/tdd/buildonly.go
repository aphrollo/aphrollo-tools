package tdd

import (
	"fmt"
	"strings"
	"time"
)

// An --example target and a --no-run bench select ZERO tests by
// construction, always — not because a filter missed, and with nothing to
// widen to. They are COMPILE checks: cargoTargetRunner already says so for a
// bench ("a bench RUN costs minutes and says nothing about correctness; the
// question an edit asks is whether it still compiles"), and an example is a
// binary with a main(), not test code, so it gets the same --no-run
// treatment. What was missing is the state: both used to render through
// greenLabel, and under nextest an --example run exits 4 with "no tests to
// run", which printed
//
//	green (0 tests — nothing to run, 1.4s)
//
// — a build result wearing a test verdict's clothes. A build-only run says
// what it is, and joins the inconclusive family: nothing was tested, so it
// must not arm the rerun block either.

// BuildOnly is the verdict for a run that COMPILED a target and ran no
// tests. Inconclusive family, alongside NoTestsSelected, TIMEOUT/SKIPPED/
// QUEUED-SKIPPED, DeferredAbandoned and InfraFailed — deliberately not a
// shape isSettledVerdict recognises.
const BuildOnly = "build-only"

// cargoBuildOnlyFlags name a cargo target that runs no tests.
var cargoBuildOnlyFlags = map[string]bool{"--example": true, "--bench": true}

// buildOnlyRunner reports whether r asks cargo to COMPILE a target rather
// than run tests.
func buildOnlyRunner(r Runner) bool {
	if r.Cmd != "cargo" {
		return false
	}
	for _, a := range r.Args {
		if cargoBuildOnlyFlags[flagName(a)] {
			return true
		}
	}
	return false
}

// untestedVerdict names what a non-failing run proved when it proved
// nothing: BuildOnly for a compile check, NoTestsSelected for a scope that
// came back empty, "" for a run that actually executed tests. One predicate,
// so the edit hook, the deferred hook and the commit/merge stages cannot
// drift on what "nothing was tested" means.
func untestedVerdict(r Runner, res SuiteResult) string {
	switch {
	case buildOnlyRunner(r) && res.Passed:
		return BuildOnly
	case selectedZeroTests(r, res):
		return NoTestsSelected
	}
	return ""
}

// buildOnlyTerminal logs and renders the line that ENDS the hook for a
// compile check that came back clean, "" for anything else — a run that is
// not build-only at all, or a build-only target that FAILED to compile,
// which is a real failure about the code just edited and stays red.
func buildOnlyTerminal(r Runner, root string, res SuiteResult) string {
	if untestedVerdict(r, res) != BuildOnly {
		return ""
	}
	appendGateLog("postedit", root, cmdString(r), BuildOnly, res.Duration)
	return buildOnlyAdvisory(r, root, res.Duration)
}

// buildOnlyAdvisory is that line. It reads like its inconclusive siblings
// (timeoutAdvisory, noTestsSelectedAdvisory) because it means the same thing
// for the code: it compiles, and nothing tested it.
func buildOnlyAdvisory(r Runner, root string, dur time.Duration) string {
	return fmt.Sprintf("gate: %s in %s → %s (compiled clean in %.1fs, 0 tests run) — an %s target is a COMPILE check, never a test verdict; the code was NOT tested",
		cmdString(r), root, strings.ToUpper(BuildOnly), dur.Seconds(), buildOnlyTargetKind(r))
}

// buildOnlyTargetKind names which of the two kinds this run compiled, so the
// line says "an --example target" rather than making the reader re-read the
// command to find out.
func buildOnlyTargetKind(r Runner) string {
	for _, a := range r.Args {
		if cargoBuildOnlyFlags[flagName(a)] {
			return flagName(a)
		}
	}
	return "a build-only"
}

// zeroSelectionTerminal is the same shape for a run that selected no test at
// all and whose caller cannot widen it — the deferred path, which has
// already spent its whole budget on one detached run. The honest verdict
// without the retry: the direct path (resolveEmptySelection) widens first
// and only lands here when the wider run stayed empty too.
func zeroSelectionTerminal(r Runner, root string, res SuiteResult) string {
	if untestedVerdict(r, res) != NoTestsSelected {
		return ""
	}
	appendGateLog("postedit", root, cmdString(r), NoTestsSelected, res.Duration)
	return noTestsSelectedAdvisory(r, r, root, false, res.Duration)
}
