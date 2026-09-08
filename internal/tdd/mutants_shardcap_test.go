package tdd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

// The shard count is what the BOX allows, bounded by nothing else. A lane
// with two mutants on an eight-core box starts eight cargo-mutants processes,
// and the six that get an empty slice each pay their own cold baseline build
// on the first run. Correctness survives it — an empty shard exits 0 with
// mutants.json as [] and is counted as empty, not as no-verdict — but the
// wall clock does not.
func TestMeasure_CapsTheShardCountToTheMutantsInTheDiff(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(7, "pinned"))
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	// After the stub, which stands the probe down: this test is the one that
	// pins what it answers.
	t.Cleanup(setMutantsListCountForTest(2, true))
	var log strings.Builder

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log}); err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 2 {
		t.Fatalf("ran the tool %d time(s), want one process per MUTANT-bearing shard, not per core", len(*calls))
	}
	if !strings.Contains(log.String(), "2 shards") {
		t.Errorf("log = %q, want the shard count it settled on", log.String())
	}
}

// A probe that could not answer never caps: the box's own number is the
// fallback, because measuring on more shards than there are mutants wastes
// wall clock while measuring on fewer than the box allows for no reason at
// all would be a slow run nobody asked for.
func TestMeasure_KeepsTheBoxsShardCountWhenTheMutantsCannotBeListed(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	// The stub already stands the probe down; said again here because it is
	// the condition this test is about.
	t.Cleanup(setMutantsListCountForTest(0, false))

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard}); err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 3 {
		t.Fatalf("ran the tool %d time(s), want the box's own shard count when nothing said otherwise", len(*calls))
	}
}

// The count comes from the tool's OWN list, asked for with the run's own
// scoping and without building anything: `--list --json`, before the `--`
// that hands the rest to the test tool, so the repo's filterset stays a
// passthrough.
func TestCargoMutantsListCount_ReadsTheToolsOwnListWithoutBuilding(t *testing.T) {
	root := t.TempDir()
	argv := cargoMutantsArgv(MutantsArgv("/w/changed.diff", 180, []string{"a"}, "not(test(slow))"))
	var seen []string
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		seen = c.Argv
		return 0, nil
	})

	// The stand-in writes nothing, which is a probe that did not answer.
	if _, ok := mutantsListCount(context.Background(), root, MutantsConfig{}, argv, io.Discard); ok {
		t.Error("an empty answer must not be read as zero mutants — that would cap the run to one shard on nothing")
	}
	joined := strings.Join(seen, " ")
	if !strings.Contains(joined, "--list --json") {
		t.Errorf("argv = %v, want the tool's own list asked for with --list --json", seen)
	}
	if !strings.Contains(joined, "--package a") || !strings.HasSuffix(joined, "-- -E not(test(slow))") {
		t.Errorf("argv = %v, want the run's own scoping and its passthrough last", seen)
	}

	stubMutantsExec(t, func(_ context.Context, _ int, _ measuredCall) (int, error) {
		return 0, nil
	})
	// A real list is a JSON array of mutants; the tool prints it among its
	// own lines, so what is read back is the array and not the whole output.
	list := `{"schema":"noise"}` + "\n" + `[{"function":{"function_name":"a"}},{"function":{"function_name":"b"}},{"function":{"function_name":"c"}}]` + "\n"
	restore := SetMutantsExecForTest(func(_ context.Context, _ string, _, _ []string, log io.Writer) (int, error) {
		fmt.Fprint(log, list)
		return 0, nil
	})
	t.Cleanup(restore)

	n, ok := mutantsListCount(context.Background(), root, MutantsConfig{}, argv, io.Discard)
	if !ok || n != 3 {
		t.Errorf("count = (%d, %v), want the three mutants the tool listed", n, ok)
	}
}
