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
// actually tested something. A Go source edit climbs the same way, from its
// own package to the packages whose tests reach it.

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
//
// A range over the arguments rather than an index the body advances: a
// value flag's argument is consumed by marking it, so no edit to the
// bookkeeping can send the walk backwards into an endless loop. A filter
// flag's own value needs no mark — it is a bare word after a flag, which is
// the positional filter and dropped anyway.
func dropCargoNameFilter(r Runner) (Runner, bool) {
	out := make([]string, 0, len(r.Args))
	dropped, seenFlag, valueOfPrev := false, false, false
	for i, a := range r.Args {
		if valueOfPrev {
			valueOfPrev = false
			continue
		}
		name := flagName(a)
		inline := strings.Contains(a, "=")
		switch {
		case cargoNameFilterFlags[name]:
			dropped, seenFlag = true, true
		case cargoScopeValueFlags[name], cargoTargetValueFlags[name]:
			out = append(out, a)
			if !inline && i+1 < len(r.Args) {
				out = append(out, r.Args[i+1])
				valueOfPrev = true
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
//
// Only cargo and Go narrow in a way a wider run can answer. pytest narrows
// only a TEST edit, to that file, where selecting nothing means the file has
// no test yet (writing-test, correctly). vitest's `related` and jest's
// `--findRelatedTests` select by the import graph: when no test file reaches
// the edited one, the full suite holds the same test files and none of them
// reaches it either, so a wider run cannot select a test for this code.
func postEditWideningSteps(r Runner, root string) []Runner {
	switch r.Cmd {
	case "cargo":
		return cargoWideningSteps(r)
	case "go":
		return goWideningSteps(r, root)
	}
	return nil
}

// goWideningSteps is the Go ladder: one rung, to the packages whose tests
// reach the narrowed ones (goTestReachFn), minus the narrowed ones — they
// already ran and selected nothing. A `/...` selection is a test file's own
// tree, where selecting nothing is scaffolding rather than a missed filter,
// so it has no rung; neither does a reach that cannot be read, nor one that
// adds no package.
func goWideningSteps(r Runner, root string) []Runner {
	if goTestTreeSelection(r) {
		return nil
	}
	selected, ok := goSelectedDirs(r)
	if !ok {
		return nil
	}
	isSelected := map[string]bool{}
	for _, d := range selected {
		isSelected[d] = true
	}
	var extra []string
	for _, d := range selected {
		reach, err := goTestReachFn(root, d)
		if err != nil {
			return nil
		}
		for _, dir := range reach {
			if !isSelected[dir] {
				extra = append(extra, dir)
			}
		}
	}
	if len(extra) == 0 {
		return nil
	}
	args := []string{"test"}
	for _, a := range r.Args[1:] {
		if strings.HasPrefix(a, "-") {
			args = append(args, a)
		}
	}
	for _, dir := range dedupeSorted(extra) {
		if dir == "." {
			args = append(args, ".")
			continue
		}
		args = append(args, "./"+dir)
	}
	return []Runner{{Cmd: "go", Args: args, Dir: r.Dir}}
}

// goTestTreeSelection reports whether a Go runner selects a `/...` tree, the
// shape a TEST edit narrows to (NarrowToRelatedTests).
func goTestTreeSelection(r Runner) bool {
	for _, a := range r.Args {
		if strings.HasSuffix(a, "/...") {
			return true
		}
	}
	return false
}

// postEditSelectedZero is the post-edit hook's "this run tested nothing":
// cargo's zero selection (selectedZeroTests, shared with the commit stages
// and the mutation proof) or a source edit's Go run whose packages all ran
// no test. A test edit's `/...` run that ran nothing stays writing-test: its
// file has no test yet. Go is read here and not in selectedZeroTests because
// the commit stages judge a Go run by its -json stream (vacuousGoPackages).
func postEditSelectedZero(r Runner, res SuiteResult) bool {
	return selectedZeroTests(r, res) || (!goTestTreeSelection(r) && goRanNoTests(r, res))
}

// widenBudgetSpentAdvisory is the line for a ladder the post-edit budget ran
// out on: the last run selected nothing and the next rung could not start in
// the time left. It names that rung, because it is the command that answers
// what this edit's run did not.
func widenBudgetSpentAdvisory(last, next Runner, root string, dur time.Duration) string {
	return fmt.Sprintf("gate: %s in %s → %s (0 tests selected in %.1fs; the post-edit budget was spent before the wider run %s could start) — inconclusive, the code was NOT tested; run %s by hand",
		cmdString(last), root, strings.ToUpper(NoTestsSelected), dur.Seconds(), cmdString(next), cmdString(next))
}
