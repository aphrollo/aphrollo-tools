package tdd

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
)

// Seven recorded escapes, one fingerprint: the merge gate refused a lane
// whose pre-commit gate had run the same tree green, and what it printed was
//
//	mutants: the run exited 4 and reached no verdict — see ...\shard-0\...\log
//	mutants: the run exited 4294967295 and reached no verdict — ...
//	  (open ...\outcomes.json: The system cannot find the file specified.)
//
// 4294967295 is 0xFFFFFFFF, a process that was killed. In every one of them
// the shard wrote NO outcomes file, so it measured nothing — and measuring
// nothing is exactly the condition the environmental retry exists for. It did
// not fire, because it fires on the LOG TEXT and a killed process writes no
// diagnostic to match.
//
// So the trigger is the missing measurement itself, not the wording of the
// wreckage: a shard that reached no verdict at all is retried once, alone,
// after its siblings have finished and with the same wait for the box.
func TestMeasure_ShardThatWroteNoOutcomesIsRetriedAndItsRetrysVerdictCounts(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	t.Cleanup(setMutantsBoxForTest(24, 63, 0)) // free memory unreadable: never a wait
	var mu sync.Mutex
	seen := map[int]int{}
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		shard := shardIndexOf(c.Argv)
		mu.Lock()
		seen[shard]++
		attempt := seen[shard]
		mu.Unlock()
		if shard == 1 && attempt == 1 {
			// Killed mid-run: no outcomes file, and not one word in the log
			// that any signature could recognise.
			return 4294967295, nil
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
		t.Fatalf("ran the tool %d time(s), want three shards and one retry of the one that measured nothing: %+v",
			len(*calls), *calls)
	}
	if got := flagValue((*calls)[3].Argv, "--shard"); got != "1/3" {
		t.Errorf("the last run was --shard %q, want the retry of 1/3 after every other shard finished", got)
	}
	if v.Refused || v.Tested != 3 {
		t.Fatalf("verdict = %+v, want all three shards measured once the retry answered", v)
	}
}

// The other half of the claim, and the one that matters more: a shard that
// PRODUCED outcomes is a measurement. Exit 2 with survivors in the file is
// cargo-mutants reporting on the lane, and re-running it would be re-running
// a real verdict until the box happened to agree — the fastest way to turn a
// gate into a coin flip. It is refused exactly as before, on the first run.
func TestMeasure_ShardWithRealSurvivorsIsNeverRetried(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	t.Cleanup(setMutantsBoxForTest(24, 63, 0))
	survivor := MutantOutcome{File: "crates/a/src/lib.rs", Line: 2, Col: 5,
		Mutation: "replace * with /", Package: "a", Status: "missed"}
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		shard := shardIndexOf(c.Argv)
		out := []MutantOutcome{{File: "crates/a/src/lib.rs", Line: 1, Col: 30 + shard,
			Mutation: "replace + with -", Package: "a", Status: "caught"}}
		code := 0
		if shard == 1 {
			out, code = append(out, survivor), 2 // cargo-mutants' own "missed" verdict
		}
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), out...)
		return code, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if len(*calls) != 3 {
		t.Fatalf("ran the tool %d time(s), want three — a shard that reported survivors was MEASURED, "+
			"and retrying a measurement is retrying until the answer changes: %+v", len(*calls), *calls)
	}
	if !v.Refused || v.Tested != 4 {
		t.Fatalf("verdict = %+v, want the survivor refused on the first run: 4 tested, 1 missed", v)
	}
	if want := outcomeName(survivor); !strings.Contains(v.Message, want) {
		t.Errorf("message =\n%s\nwant the survivor %q named", v.Message, want)
	}
}

// Once, not until it agrees. A shard that reaches no verdict a second time
// with the box to itself has still measured nothing, so the run refuses — and
// the refusal has to SAY it was retried, or an operator reads "exited
// 4294967295 and reached no verdict" and re-runs the whole merge to find out
// what this run already knows.
func TestMeasure_ShardThatReachesNoVerdictTwiceIsRefusedAndSaysItWasRetried(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	t.Cleanup(setMutantsBoxForTest(24, 63, 0))
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		shard := shardIndexOf(c.Argv)
		if shard == 1 {
			return 4294967295, nil
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
		t.Fatalf("ran the tool %d time(s), want three shards and EXACTLY one retry: %+v", len(*calls), *calls)
	}
	if !v.Refused || v.Tested != 0 {
		t.Fatalf("verdict = %+v, want a refusal that judged nothing — a third of the lane was never measured", v)
	}
	for _, want := range []string{"shard 1/3", "exited 4294967295", "retried once", "no verdict",
		"NOT measured", mutantsShardDir(root, 1)} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message =\n%s\nwant %q in it", v.Message, want)
		}
	}
}

// A shard whose slice of the pool was empty exits 0 having written
// `mutants.json` as `[]` and no outcomes file at all. It has nothing to
// report because there was nothing to measure, which is the one honest
// no-outcomes case — retrying it would add a whole cold build to every small
// lane on a many-core box and find the same empty slice.
func TestMeasure_ShardWithNoMutantsOfItsOwnIsNotRetried(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	t.Cleanup(setMutantsBoxForTest(24, 63, 0))
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		out := flagValue(c.Argv, "--output")
		if shardIndexOf(c.Argv) == 2 {
			writeShardMutantList(t, out, 0)
			return 0, nil
		}
		writeShardMutantList(t, out, 1)
		writeOutcomesIn(t, out, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if len(*calls) != 3 {
		t.Fatalf("ran the tool %d time(s), want three — an empty slice is nothing to measure, "+
			"not a measurement that failed: %+v", len(*calls), *calls)
	}
	if v.Refused {
		t.Fatalf("verdict = %+v, want a pass", v)
	}
}
