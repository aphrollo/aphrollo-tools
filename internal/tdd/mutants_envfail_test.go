package tdd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The four signatures in issue #609's log, and the fifth the same run left
// behind. None of them is a statement about the code being measured:
//
//	error: failed to create encoded metadata from file: The paging file is
//	   too small for this operation to complete. (os error 1455)
//	error: linking with `rust-lld.exe` failed: exit code: 0xc0000142
//	error: only metadata stub found for `rlib` dependency `core`
//	error[E0786]: found invalid metadata files for crate `serde`
//	a rustc internal compiler error backtrace, where the seventh shard's
//	   baseline line should have been
//
// The last three are the WRECKAGE of the first two: a rustc killed for memory
// leaves a half-written rlib behind, and the next compilation reads it as a
// metadata stub. A run that reports any of them measured nothing, and calling
// it a verdict is calling the box's exhaustion a property of the lane.
//
// The other half of the claim matters as much: a real compile error and a
// real test failure are the lane's, and must never be excused as the box's.
func TestMutantsEnvironmentalBuildFailure_NamesTheBoxsFailuresAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		output string
		want   bool
	}{
		{name: "pagefile exhausted", want: true,
			output: "error: failed to create encoded metadata from file: The paging file is too small for this operation to complete. (os error 1455)\n"},
		{name: "child could not start", want: true,
			output: "error: linking with `rust-lld.exe` failed: exit code: 0xc0000142\n"},
		{name: "metadata stub left by a killed rustc", want: true,
			output: "error: only metadata stub found for `rlib` dependency `core` please provide path to the corresponding .rmeta file with full metadata\n"},
		{name: "invalid metadata files", want: true,
			output: "error[E0786]: found invalid metadata files for crate `serde`\n"},
		{name: "rustc internal compiler error", want: true,
			output: "error: internal compiler error: unexpected panic\nstack backtrace:\n   9: rustc_interface::util::run_in_thread_with_globals\n"},
		{name: "a real compile error is the lane's", want: false,
			output: "error[E0308]: mismatched types\n  --> crates/a/src/lib.rs:1:36\nerror: could not compile `a` (lib) due to 1 previous error\n"},
		{name: "a real test failure is the lane's", want: false,
			output: "FAIL [   0.004s] a::t adds\nSummary [   0.1s] 1 test run: 0 passed, 1 failed\n"},
		{name: "nothing at all", want: false, output: ""},
	} {
		signature, got := mutantsEnvironmentalBuildFailure(c.output)
		if got != c.want {
			t.Errorf("%s: environmental = %v (%q), want %v", c.name, got, signature, c.want)
		}
		if got && signature == "" {
			t.Errorf("%s: an environmental failure with no signature to name has nothing to report", c.name)
		}
	}
}

// A shard whose BUILD died of the box was never a measurement, so it is not a
// result: it is retried once, alone, after every other shard has finished and
// the box is quiet again. The retry gets the whole box's build width, exactly
// as the lone timeout re-run does, for the same reason — a retry under the
// same contention that killed it is not a second chance.
//
// And its build directory is cleaned FIRST. A rustc killed mid-write leaves a
// metadata stub where a compiled rlib should be, and reusing that directory
// reproduces `only metadata stub found for rlib dependency core` on a run
// that would otherwise be clean.
func TestMeasure_EnvironmentalShardFailureIsRetriedAloneOnACleanBuildDir(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	t.Cleanup(setMutantsBoxForTest(24, 63, 0)) // the box issue #609 died on, free memory unreadable
	poison := filepath.Join(mutantsShardTargetDir(root, 1), "debug", "deps", "libcore.rmeta")
	var mu sync.Mutex
	seen := map[int]int{}
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		shard := shardIndexOf(c.Argv)
		mu.Lock()
		seen[shard]++
		attempt := seen[shard]
		mu.Unlock()
		if shard == 1 && attempt == 1 {
			mustWrite(t, poison, "half a rustc\n")
			fmt.Fprint(c.Log, "FAILED   Unmutated baseline in 396s build\n"+
				"error: failed to create encoded metadata from file: The paging file is too small for this operation to complete. (os error 1455)\n")
			return 4, nil
		}
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 30 + shard, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if len(*calls) != 4 {
		t.Fatalf("ran the tool %d time(s), want three shards and one retry: %+v", len(*calls), *calls)
	}
	last := (*calls)[3]
	if got := flagValue(last.Argv, "--shard"); got != "1/3" {
		t.Errorf("the last run was --shard %q, want the retry of 1/3 after every other shard finished", got)
	}
	// The whole box for the retry, a third of it for the shards that ran
	// together: min(cores 24, ram 63GB/6GB=10) is 10 jobs cold, which is 3
	// each across three shards and all 10 for a run that has the box.
	if got := envValueOf(last.Env, "CARGO_BUILD_JOBS"); got != "10" {
		t.Errorf("the retry built %s jobs wide, want the whole box's 10 — a retry under the contention "+
			"that killed it is not a second chance", got)
	}
	for i, c := range (*calls)[:3] {
		if got := envValueOf(c.Env, "CARGO_BUILD_JOBS"); got != "3" {
			t.Errorf("shard call %d built %s jobs wide, want 3", i, got)
		}
	}
	if _, err := os.Stat(poison); err == nil {
		t.Errorf("%s survived into the retry: a build dir a killed rustc wrote to is reused as a metadata stub", poison)
	}
	if v.Refused || v.Tested != 3 {
		t.Fatalf("verdict = %+v, want all three shards measured once the retry answered", v)
	}
}

// Retried ONCE. A shard that hits the box's own failure again with the box to
// itself has not been measured at all, and the run says so: the same refusal
// the failing run already produced — which shard, its log, no verdict — with
// the reason it reached no verdict named, so nobody reads an exhausted box as
// a lane that survived. And the poisoned directory does not outlive the run.
func TestMeasure_ShardThatFailsEnvironmentallyTwiceIsRefusedAsUnmeasured(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	poison := filepath.Join(mutantsShardTargetDir(root, 1), "debug", "deps", "libcore.rmeta")
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		shard := shardIndexOf(c.Argv)
		if shard == 1 {
			mustWrite(t, poison, "half a rustc\n")
			fmt.Fprint(c.Log, "error: only metadata stub found for `rlib` dependency `core`\n")
			return 4, nil
		}
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 30 + shard, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if len(*calls) != 4 {
		t.Fatalf("ran the tool %d time(s), want three shards and exactly one retry", len(*calls))
	}
	if !v.Refused || v.Tested != 0 {
		t.Fatalf("verdict = %+v, want a refusal that judged nothing: shard 1's mutants were never measured", v)
	}
	for _, want := range []string{"shard 1/3", "exited 4", "environmental", "NOT measured", mutantsShardDir(root, 1)} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message =\n%s\nwant %q in it", v.Message, want)
		}
	}
	if _, err := os.Stat(poison); err == nil {
		t.Errorf("%s outlived the run: the next run would build on a killed rustc's leavings", poison)
	}
}
