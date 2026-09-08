package tdd

import (
	"context"
	"strings"
	"testing"
)

// git prints advice on STDERR, and advice is not a change to the tree. The
// tree-changed guard read the COMBINED output, so the first `git diff` after
// cargo-mutants rewrote a source file — which on a box with core.autocrlf on
// and LF bytes in the worktree adds "warning: in the working copy of
// 'src/lib.rs', LF will be replaced by CRLF the next time Git touches it" —
// no longer matched the snapshot taken before the run. The measurement was
// refused with an EMPTY `--stat`, no file named and every mutant caught: a
// merge blocked by a line-ending warning.
//
// Closed form: the same patch on stdout before and after, a warning on
// stderr only afterwards. What decides the verdict is stdout alone.
func TestMeasure_GitWarningOnStderrIsNotATreeChange(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	const patch = "diff --git a/crates/a/src/lib.rs b/crates/a/src/lib.rs\n@@ -1 +1 @@\n-a + b\n+a - b\n"
	diffs := 0
	stubGitDiff(t, func(args []string) (string, string, error, bool) {
		switch {
		case len(args) == 2 && args[1] == "--stat":
			return " crates/a/src/lib.rs | 1 +\n", "", nil, true
		case len(args) == 1 && args[0] == "diff":
			diffs++
			if diffs == 1 {
				return patch, "", nil, true
			}
			// Every look after the run carries the advice, and the tree it
			// describes is byte for byte the one that went in.
			return patch, warningOnStderr, nil, true
		}
		return "", "", nil, false
	})
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("verdict = %+v, want a pass: git said the same patch both times and warned about line endings on stderr", v)
	}
	if diffs < 2 {
		t.Fatalf("the tree was read %d time(s), want it snapshotted before the run and read again after it", diffs)
	}
	if v.Caught != 1 {
		t.Errorf("Caught = %d, want the one caught mutant judged", v.Caught)
	}
}

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
	calls := stubMutantsExec(t, func(_ context.Context, n int, _ measuredCall) (int, error) {
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
