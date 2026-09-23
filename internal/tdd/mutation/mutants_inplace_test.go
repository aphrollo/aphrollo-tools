package mutation

import (
	"context"
	"path/filepath"
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

// `--in-place` mutates the one checkout and so forbids `--jobs` (cargo-mutants
// refuses the pair, issue #592), which made the run one job by construction:
// 739 mutants in 16 h on a box that could measure eight at once. So --in-place
// is absent from the flag set — and so is --jobs, which belongs to the shard
// that carries it (mutantsShardArgv), never to the flags every shard shares.
//
// What the copy carries is the SOURCE TREE ONLY. `--copy-target=true` carried
// the workspace target dir into every copy: 375 GB per job on a real repo,
// 1 h 47 min for the first copy, a full drive and no verdict.
func TestMutantsArgv_NeverPassesJobsWithInPlace(t *testing.T) {
	t.Parallel()
	argv := MutantsArgv("/w/changed.diff", 120, []string{"a"}, "not(test(slow))")
	sourceOnly := false
	for _, arg := range argv {
		if arg == "--in-place" {
			t.Errorf("argv = %v, want no --in-place: the run copies the tree so its shards can run side by side", argv)
		}
		if arg == "--jobs" {
			t.Errorf("argv = %v, want no --jobs in the shared flags: one process per shard carries its own", argv)
		}
		if arg == "--copy-target=true" {
			t.Errorf("argv = %v, want no --copy-target=true: copying the target dir is what cost 375 GB a job", argv)
		}
		if arg == "--copy-target=false" {
			sourceOnly = true
		}
	}
	if !sourceOnly {
		t.Errorf("argv = %v, want --copy-target=false: the copy is the source tree, and the warm build "+
			"products live in the shard's own persistent target dir", argv)
	}
}

// The lone re-run of a timed-out mutant is the run's argv plus a name filter,
// and nothing else — pinned here as a closed form, because the two ways to
// get it wrong are both silent. It used to rewrite `--jobs` to 1, a flag that
// must not appear in an in-place argv at all (issue #592); and now that the
// run is N sharded processes, a `--shard i/n` inherited from one of them
// would re-run a FRACTION of the named mutants and leave the rest timed out
// for a reason that has nothing to do with them.
func TestMutantsRerun_AddsOnlyTheNameFilterAndNeverAShardFlag(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	slow := MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "timeout"}
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		out := c.Argv[len(c.Argv)-1]
		if strings.HasPrefix(out, "^") {
			// The re-run, which settles the mutant it names.
			settled := slow
			settled.Status = "caught"
			writeOutcomesIn(t, mutantsShardDir(root, 0), settled)
			return 0, nil
		}
		if shardIndexOf(c.Argv) == 0 {
			writeOutcomesIn(t, flagValue(c.Argv, "--output"), slow)
			return 0, nil
		}
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 2, Col: 5, Mutation: "replace * with /", Package: "a", Status: "caught"})
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if len(*calls) != 3 {
		t.Fatalf("ran the tool %d time(s), want two shards plus one lone re-run", len(*calls))
	}
	rerun := (*calls)[2].Argv
	want := []string{
		"cargo", "mutants", "--copy-target=false", "--in-diff",
		filepath.Join(measureTempDir(root), "changed.diff"), "--no-shuffle", "--test-tool=nextest",
		"--minimum-test-timeout", "120", "--timeout-multiplier", "3", "--package", "a",
		"--jobs", "1", "--output", mutantsShardDir(root, 0),
		"--re", `^crates/a/src/lib\.rs:1:36: replace \+ with -$`,
	}
	if strings.Join(rerun, " ") != strings.Join(want, " ") {
		t.Fatalf("re-run argv =\n  %v\nwant the run's own argv, unsharded, plus only the name filter\n  %v", rerun, want)
	}
}
