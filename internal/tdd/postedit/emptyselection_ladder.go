package postedit

import (
	"fmt"
	"path/filepath"
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

// postEditWideningSteps is the ladder for any post-edit runner: the rungs to
// climb, in order, when that runner selected no test.
//
// Only cargo and Go narrow in a way a wider run can answer. pytest narrows
// only a TEST edit, to that file, where selecting nothing means the file has
// no test yet (writing-test, correctly). vitest's `related` and jest's
// `--findRelatedTests` select by the import graph: when no test file reaches
// the edited one, the full suite holds the same test files and none of them
// reaches it either, so a wider run cannot select a test for this code.
func postEditWideningSteps(r Runner, target, root string) []Runner {
	switch r.Cmd {
	case "cargo":
		return cargoWideningSteps(r)
	case "go":
		return goWideningSteps(r, target, root)
	}
	return nil
}

// goWideningSteps is the Go ladder: one rung, to the packages whose tests
// reach the narrowed ones (goTestReachFn), minus the narrowed ones — they
// already ran and selected nothing. A test file's own run (goOwnTestRun)
// is one where selecting nothing is scaffolding rather than a missed filter,
// so it has no rung; neither does a reach that cannot be read, nor one that
// adds no package.
func goWideningSteps(r Runner, target, root string) []Runner {
	if r.Select != nil && r.Select.Reason == "" && len(r.Args) > 1 {
		// test-select named tests the build does not hold: the first rung is the
		// package whole, and the ladder goes on from there. Keeping the -run
		// filter on the wider rungs would select nothing again.
		whole := r
		whole.Args = []string{r.Args[0], r.Args[1]}
		whole.Select = &Selection{Total: r.Select.Total, Reason: "the selected tests ran none"}
		return append([]Runner{whole}, goWideningSteps(whole, target, root)...)
	}
	if goOwnTestRun(r, target) {
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
	rung := r
	rung.Args = args
	return []Runner{rung}
}

// goOwnTestRun reports whether r is the run a Go TEST edit owes: the edited
// file is a _test.go file (NarrowToRelatedTests narrows it to its package), or
// r selects a `/...` tree, the form a test-classified non-Go file narrows to.
func goOwnTestRun(r Runner, target string) bool {
	return r.Cmd == "go" && strings.HasSuffix(filepath.ToSlash(target), "_test.go") || goTestTreeSelection(r)
}

// goTestTreeSelection reports whether a Go runner selects a `/...` tree.
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
// no test. A test edit's own run that ran nothing stays writing-test: its
// file has no test yet. Go is read here and not in selectedZeroTests because
// the commit stages judge a Go run by its -json stream (vacuousGoPackages).
func postEditSelectedZero(r Runner, target string, res SuiteResult) bool {
	return selectedZeroTests(r, res) || (!goOwnTestRun(r, target) && goRanNoTests(r, res))
}

// widenBudgetSpentAdvisory is the line for a ladder the post-edit budget ran
// out on: the last run selected nothing and the next rung could not start in
// the time left. It names that rung, because it is the command that answers
// what this edit's run did not.
func widenBudgetSpentAdvisory(last, next Runner, root string, dur time.Duration) string {
	return fmt.Sprintf("gate: %s in %s → %s (0 tests selected in %.1fs; the post-edit budget was spent before the wider run %s could start) — inconclusive, the code was NOT tested; run %s by hand",
		cmdString(last), root, strings.ToUpper(NoTestsSelected), dur.Seconds(), cmdString(next), cmdString(next))
}
