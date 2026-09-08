package tdd

import (
	"context"
	"io"
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

// The run copies the tree per job and mutates the copies: `--in-place`
// mutates the one checkout and so forbids `--jobs` (cargo-mutants refuses
// the pair, issue #592), which measured 739 mutants in 16 h on a box that
// could run eight copies. So: --in-place is absent from the flag set, and
// the run itself carries the box's job count.
func TestMutantsArgv_NeverPassesJobsWithInPlace(t *testing.T) {
	t.Parallel()
	argv := MutantsArgv("/w/changed.diff", 120, []string{"a"}, "not(test(slow))")
	warm := false
	for _, arg := range argv {
		if arg == "--in-place" {
			t.Errorf("argv = %v, want no --in-place: the run copies the tree so its jobs can run side by side", argv)
		}
		if arg == "--copy-target=true" {
			warm = true
		}
	}
	if !warm {
		t.Errorf("argv = %v, want --copy-target=true: every copy must start from the lane's warm target dir, never build cold", argv)
	}
}

// The measurement hands cargo-mutants the box's job count: the whole point
// of copying the tree is that the copies are measured at the same time.
func TestMeasure_CargoRunCarriesTheBoxJobCount(t *testing.T) {
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	root, base := measureFixture(t, laneSource)
	calls := stubMutantsExec(t, func(_ context.Context, _ int, _ measuredCall) (int, error) {
		writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard}); err != nil {
		t.Fatal(err)
	}
	argv := (*calls)[0].Argv
	for i, arg := range argv {
		if arg == "--jobs" && i+1 < len(argv) && argv[i+1] == "3" {
			return
		}
	}
	t.Errorf("argv = %v, want --jobs 3 from the box seam", argv)
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
