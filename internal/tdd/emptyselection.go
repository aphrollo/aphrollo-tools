package tdd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A post-edit run is NARROWED on purpose: cargoTargetRunner maps a src edit
// to `--lib` plus one module's filter so the edit-time signal stays fast,
// with the mechanical suite at the merge. What went wrong is not the
// narrowing, it is what a narrowed run that selected NOTHING was allowed to
// say. A crate whose tests are integration tests (tests/*.rs, separate test
// binaries, module paths that never start with the src module) matches
// nothing under `--lib`; nextest then exits 4 with "no tests to run",
// treatAsEmptyPass converts that to a PASS, and the advisory read
//
//	green (0 tests — nothing to run, 1.3s)
//
// for code no test had touched. Reported from the field, where it swallowed
// four lanes' worth of test runs and both builders fell back to proving
// their work by hand.
//
// Two facts, one line: "this filter selected nothing" is not "this crate has
// no tests". This file separates them — widen once and let the wider run
// answer, and when nothing is selected even then, say so in the inconclusive
// family's own words instead of in green's.

// NoTestsSelected is the verdict for a run that executed ZERO tests.
// Inconclusive family, alongside TIMEOUT/SKIPPED/QUEUED-SKIPPED,
// DeferredAbandoned and InfraFailed — the code was NOT tested — and
// deliberately NOT a shape isSettledVerdict recognises: a run that tested
// nothing must not silence the hand-run that would have caught it.
const NoTestsSelected = "no-tests-selected"

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

// widenCargoRunner drops a narrowed cargo run's within-package narrowing and
// keeps its package scope, reporting false when there was nothing to drop
// (the run is already as wide as this crate goes) or when the runner is a
// build-only target — an example or a bench selects zero tests BY
// CONSTRUCTION, and widening it into the package's whole suite would cost
// minutes to answer a question nobody asked.
func widenCargoRunner(r Runner) (Runner, bool) {
	if r.Cmd != "cargo" || buildOnlyRunner(r) {
		return Runner{}, false
	}
	out := make([]string, 0, len(r.Args))
	dropped, seenFlag := false, false
	for i := 0; i < len(r.Args); i++ {
		a := r.Args[i]
		name := flagName(a)
		hasInlineValue := strings.Contains(a, "=")
		switch {
		case cargoNarrowingBareFlags[name], cargoNarrowingValueFlags[name]:
			if cargoNarrowingValueFlags[name] && !hasInlineValue {
				i++
			}
			dropped, seenFlag = true, true
		case cargoScopeValueFlags[name]:
			out = append(out, a)
			if !hasInlineValue && i+1 < len(r.Args) {
				out = append(out, r.Args[i+1])
				i++
			}
			seenFlag = true
		case strings.HasPrefix(a, "-"):
			out = append(out, a)
			seenFlag = true
		case seenFlag:
			// A bare word after a flag is cargo's positional name filter;
			// the verb words (`test`, `nextest run`) come first and are
			// kept.
			dropped = true
		default:
			out = append(out, a)
		}
	}
	if !dropped {
		return Runner{}, false
	}
	return Runner{Cmd: r.Cmd, Args: out, Dir: r.Dir}, true
}

// emptySelection is what resolveEmptySelection hands back: the runner and
// result the caller should go on judging (the widened pair when widening
// produced an answer), a note naming the widening for the eventual advisory,
// and a terminal advisory the caller must return as-is — a timeout, a
// build-lock skip, or the inconclusive line for a selection that stayed
// empty.
type emptySelection struct {
	runner   Runner
	res      SuiteResult
	note     string
	terminal string
}

// resolveEmptySelection widens a narrowed run that selected nothing, ONCE.
// The widened run inherits the same lock/timeout handling as the first
// (runPostEditSuite), and its verdict is the one that stands: the crate's
// tests exist, they are just not where the filter looked. When nothing is
// selected even at package scope — or there was no narrowing left to drop —
// the run reaches the inconclusive verdict rather than a green, and that is
// what reaches gate.log.
func resolveEmptySelection(run SuiteRunner, snap stateSnapshot, root, headSHA string, res SuiteResult) emptySelection {
	out := emptySelection{runner: snap.runner, res: res}
	narrow := snap.runner
	widened := false
	if wide, ok := widenCargoRunner(narrow); ok {
		widened = true
		wsnap := snap
		wsnap.runner = wide
		wres, terminal := runPostEditSuite(run, wsnap, root, headSHA)
		if terminal != "" {
			out.terminal = terminal
			return out
		}
		out.runner, out.res = wide, wres
		if !selectedZeroTests(wide, wres) {
			out.note = widenedNote(narrow)
			return out
		}
	}
	appendGateLog("postedit", root, cmdString(out.runner), NoTestsSelected, out.res.Duration)
	out.terminal = noTestsSelectedAdvisory(narrow, out.runner, root, widened, out.res.Duration)
	return out
}

// widenedNote is the second line under a widened run's ordinary advisory. The
// verdict above it is real, but WHICH tests produced it is not what the
// session asked for, so the line names the filter that selected nothing and
// where those tests most likely live.
func widenedNote(narrow Runner) string {
	return fmt.Sprintf("widened: %s selected 0 tests, so the run was re-scoped to the whole package for this verdict — the tests this edit's module owns are likely integration tests under tests/",
		cmdString(narrow))
}

// noTestsSelectedAdvisory is the line for a run that selected nothing and
// stayed empty when widened. It reads like its inconclusive siblings
// (timeoutAdvisory, queuedSkippedAdvisory) because it means the same thing —
// the code was NOT tested — and it says what to do about it: a crate whose
// tests are integration tests under tests/ needs its own run, and a crate
// with no test target at all needs to be read as exactly that.
func noTestsSelectedAdvisory(narrow, wide Runner, root string, widened bool, dur time.Duration) string {
	scope := ""
	if widened {
		scope = fmt.Sprintf("; %s selected none either, so this run was widened to the whole package", cmdString(narrow))
	}
	return fmt.Sprintf("gate: %s in %s → %s (0 tests selected in %.1fs%s) — inconclusive, the code was NOT tested; run this crate's integration tests under tests/ by hand, or confirm it has a test target at all",
		cmdString(wide), root, strings.ToUpper(NoTestsSelected), dur.Seconds(), scope)
}
