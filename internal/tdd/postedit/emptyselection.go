package postedit

import (
	"fmt"
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

// resolveEmptySelection climbs the widening ladder (postEditWideningSteps)
// above a narrowed run that selected nothing, one rung at a time, inside the
// post-edit budget the narrowed run already drew on. Each rung inherits the
// same lock/timeout handling as the first (runPostEditSuite), and the first
// rung that selects a test is the verdict that stands: the crate's tests
// exist, they are just not where the filter looked. A rung the budget cannot
// start is named as not run; a selection still empty at the top of the
// ladder — or a run with no ladder at all — reaches the inconclusive verdict
// rather than a green, and that is what reaches gate.log.
func resolveEmptySelection(run SuiteRunner, snap stateSnapshot, root, headSHA string, res SuiteResult) emptySelection {
	out := emptySelection{runner: snap.runner, res: res}
	narrow := snap.runner
	deadline := time.Now().Add(PostEditBudget() - res.Duration)
	steps := postEditWideningSteps(narrow, root)
	for _, step := range steps {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			AppendGateLog("postedit", root, cmdString(out.runner), NoTestsSelected, out.res.Duration)
			out.terminal = widenBudgetSpentAdvisory(out.runner, step, root, out.res.Duration)
			return out
		}
		wsnap := snap
		wsnap.runner = step
		wres, terminal := runPostEditSuite(run, wsnap, root, headSHA, remaining)
		if terminal != "" {
			out.terminal = terminal
			return out
		}
		out.runner, out.res = step, wres
		if !postEditSelectedZero(step, wres) {
			out.note = widenedNote(narrow, step)
			return out
		}
	}
	AppendGateLog("postedit", root, cmdString(out.runner), NoTestsSelected, out.res.Duration)
	out.terminal = noTestsSelectedAdvisory(narrow, out.runner, root, len(steps) > 0, out.res.Duration)
	return out
}

// cargoFullSuiteAlreadyGreenLine is the stand-down for a narrowed cargo
// runner whose WIDENED (package-scope) form already proved green at this
// exact worktree state — the shape left behind when the precommit/premerge
// stage just ran the crate's full suite and cached it (mechCacheAdd in
// runSuiteStage). This repo's own module-size law puts a file's tests in a
// SIBLING module the file's own module-path filter can never match, so every
// narrowed post-edit run there widens to the identical package-scope command
// the mechanical stage just cached — and reported "the code was NOT tested"
// two lines after that crate's suite ran green (issue #715). It is false:
// the crate WAS tested, by the wider command this exact state already
// proved. "" when nothing is cached, which is the ordinary case, so the
// narrowed run proceeds exactly as before.
func cargoFullSuiteAlreadyGreenLine(r Runner, root string) string {
	if r.Cmd != "cargo" {
		return ""
	}
	full := r
	if wide, widened := widenCargoRunner(r); widened {
		full = wide
	}
	h := worktreeStateHash(root)
	if h == "" {
		return ""
	}
	if !mechCacheHit(mechKey(root, h, full)) {
		return ""
	}
	AppendGateLog("postedit", root, cmdString(full), "cache-hit", 0)
	return fmt.Sprintf("gate: %s in %s → cache-hit (this crate's suite already verified green in this exact state; not re-run)",
		cmdString(full), root)
}

// widenedNote is the second line under a widened run's ordinary advisory. The
// verdict above it is real, but WHICH tests produced it is not what the
// session asked for, so the line names the filter that selected nothing and
// the rung whose run the verdict is.
func widenedNote(narrow, wide Runner) string {
	return fmt.Sprintf("widened: %s selected 0 tests, so the run climbed to %s for this verdict — the tests this edit's module owns live outside the narrowed selection",
		cmdString(narrow), cmdString(wide))
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
		scope = fmt.Sprintf("; widened rung by rung from %s, and no rung selected a test", cmdString(narrow))
	}
	return fmt.Sprintf("gate: %s in %s → %s (0 tests selected in %.1fs%s) — inconclusive, the code was NOT tested; %s",
		cmdString(wide), root, strings.ToUpper(NoTestsSelected), dur.Seconds(), scope, noTestsSelectedRemedy(wide))
}

// noTestsSelectedRemedy is what to do about a selection that stayed empty,
// in the runner's own terms: a crate is asked for its integration tests or
// its test target, a Go package for a test, since no package's tests reach
// it.
func noTestsSelectedRemedy(r Runner) string {
	if r.Cmd == "go" {
		return "no package's tests reach this code, so it needs a test of its own"
	}
	return "run this crate's integration tests under tests/ by hand, or confirm it has a test target at all"
}
