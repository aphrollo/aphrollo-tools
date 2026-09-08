package tdd

import (
	"strings"
	"testing"
)

// cargo-mutants REFUSES `--jobs` together with `--in-place`: an in-place run
// mutates the single tree it is measuring, so there is no second tree for a
// second job to work on. Emitting both killed the first real pre-merge
// measurement on a Cargo consumer before its first mutant —
// "error: the argument '--in-place' cannot be used with '--jobs <JOBS>'" —
// and the argv had only ever been exercised through the exec seam (issue
// #592). So: --in-place is present and --jobs is absent, in every position
// and every spelling.
func TestMutantsArgv_NeverPassesJobsWithInPlace(t *testing.T) {
	t.Parallel()
	argv := MutantsArgv("/w/changed.diff", 120, []string{"a"}, "not(test(slow))")

	inPlace := false
	for _, arg := range argv {
		if arg == "--in-place" {
			inPlace = true
		}
		if arg == "--jobs" || strings.HasPrefix(arg, "--jobs=") || arg == "-j" {
			t.Errorf("argv carries %q beside --in-place, which cargo-mutants refuses: %v", arg, argv)
		}
	}
	if !inPlace {
		t.Errorf("argv = %v, want --in-place: the run must never copy the tree", argv)
	}
}

// The lone re-run of a timed-out mutant is the first run's argv plus a name
// filter, and nothing else. It used to rewrite `--jobs` to 1 — a flag that
// must not appear in an in-place argv at all — so what it hands the tool is
// pinned here as a closed form: exactly the argv that ran, plus `--re`
// (issue #592).
func TestMutantsRerun_KeepsInPlaceAndAddsOnlyTheNameFilter(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	slow := MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "timeout"}
	calls := stubMutantsExec(t, func(n int, _ measuredCall) (int, error) {
		m := slow
		if n == 2 {
			m.Status = "caught"
		}
		writeOutcomes(t, root, m)
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if len(*calls) != 2 {
		t.Fatalf("ran the tool %d time(s), want the run plus one lone re-run", len(*calls))
	}
	first, rerun := (*calls)[0].Argv, (*calls)[1].Argv
	want := append(append([]string{}, first...), "--re", `^crates/a/src/lib\.rs:1:36: replace \+ with -$`)
	if strings.Join(rerun, " ") != strings.Join(want, " ") {
		t.Fatalf("re-run argv =\n  %v\nwant the first run's argv plus only the name filter\n  %v", rerun, want)
	}
}
