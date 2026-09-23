package tdd

import (
	"fmt"
	"strings"
	"time"
)

// Issue #730: a narrowed post-edit run that selected nothing used to widen
// once, straight to the whole package, and only on the foreground path; the
// deferred path the real hook takes did not widen at all and printed
// NO-TESTS-SELECTED, so a rename across a crate was tested by a hand run
// instead of by the hook. The rule now: the empty selection climbs a ladder,
// one rung at a time, inside the SAME post-edit budget, and only a selection
// that stays empty at the top, or a budget that runs out first, is reported
// inconclusive. A rung is only ever climbed because the one below it
// selected nothing, so the verdict always comes from the narrowest run that
// actually tested something.

// cargoNameFilterFlags are the name filters within a cargo target: nextest's
// filter expression in both spellings. Plain cargo test's positional
// substring is the other one, recognised by position in dropCargoNameFilter.
var cargoNameFilterFlags = map[string]bool{"-E": true, "--filter-expr": true}

// cargoTargetValueFlags select a target by name and keep it on the lib rung.
var cargoTargetValueFlags = map[string]bool{"--test": true, "--bin": true}

// cargoWideningSteps is the ladder above a narrowed cargo run: the same
// target with its name filter dropped (a module filter's crate lib), then
// the whole package (widenCargoRunner). A rung that would repeat the one
// below it is left out, and a run with nothing to drop, or a build-only
// target, has no ladder at all.
func cargoWideningSteps(r Runner) []Runner {
	wide, ok := widenCargoRunner(r)
	if !ok {
		return nil
	}
	var steps []Runner
	if target, dropped := dropCargoNameFilter(r); dropped && cmdString(target) != cmdString(wide) {
		steps = append(steps, target)
	}
	return append(steps, wide)
}

// dropCargoNameFilter removes a cargo run's name filter and keeps its target
// selection and package scope, reporting false when the run carried no name
// filter at all.
func dropCargoNameFilter(r Runner) (Runner, bool) {
	out := make([]string, 0, len(r.Args))
	dropped, seenFlag := false, false
	for i := 0; i < len(r.Args); i++ {
		a := r.Args[i]
		name := flagName(a)
		inline := strings.Contains(a, "=")
		switch {
		case cargoNameFilterFlags[name]:
			if !inline {
				i++
			}
			dropped, seenFlag = true, true
		case cargoScopeValueFlags[name], cargoTargetValueFlags[name]:
			out = append(out, a)
			if !inline && i+1 < len(r.Args) {
				out = append(out, r.Args[i+1])
				i++
			}
			seenFlag = true
		case strings.HasPrefix(a, "-"):
			out = append(out, a)
			seenFlag = true
		case seenFlag:
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

// postEditWideningSteps is the ladder for any post-edit runner: the rungs to
// climb, in order, when that runner selected no test.
func postEditWideningSteps(r Runner) []Runner {
	if r.Cmd == "cargo" {
		return cargoWideningSteps(r)
	}
	return nil
}

// widenBudgetSpentAdvisory is the line for a ladder the post-edit budget ran
// out on: the last run selected nothing and the next rung could not start in
// the time left. It names that rung, because it is the command that answers
// what this edit's run did not.
func widenBudgetSpentAdvisory(last, next Runner, root string, dur time.Duration) string {
	return fmt.Sprintf("gate: %s in %s → %s (0 tests selected in %.1fs; the post-edit budget was spent before the wider run %s could start) — inconclusive, the code was NOT tested; run %s by hand",
		cmdString(last), root, strings.ToUpper(NoTestsSelected), dur.Seconds(), cmdString(next), cmdString(next))
}
