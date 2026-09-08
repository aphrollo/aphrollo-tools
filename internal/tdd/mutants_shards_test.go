package tdd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ratchet: test_removed TestMeasure_CargoRunCarriesTheBoxJobCount: the box's number is the SHARD count now, not `--jobs` — one cargo-mutants process per shard, each with `--jobs 1` — and TestMeasure_RunsOneProcessPerShardEachWithItsOwnPersistentTargetDir below makes the same claim against the design that replaced it
// ratchet: test_removed TestMutantsRerun_KeepsInPlaceAndAddsOnlyTheNameFilter: renamed to TestMutantsRerun_AddsOnlyTheNameFilterAndNeverAShardFlag, same closed form plus the shard flags a lone re-run must not inherit

// Every shard is the whole run's argv with a shard of its own — never a
// narrower command line. `--package` scopes the unmutated BASELINE as well as
// the mutant pool, and each shard runs its own baseline, so a shard that lost
// it would mutate and baseline the entire workspace: hours of work that looks
// like it is working, the same class of failure as the 375 GB copy this
// design replaced. The repo's `-- -E <filterset>` has to stay LAST, because
// everything after `--` is cargo-mutants' passthrough to the test tool.
func TestMutantsShardArgv_CarriesTheFullScopingOnEveryShard(t *testing.T) {
	t.Parallel()
	const filterset = "not(test(slow))"
	base := cargoMutantsArgv(MutantsArgv("/w/changed.diff", 180, []string{"a", "b"}, filterset))
	var argvs [][]string
	for i := range 3 {
		argvs = append(argvs, mutantsShardArgv(base, i, 3, "/w/shard-"+strconv.Itoa(i)))
	}

	for i, argv := range argvs {
		joined := strings.Join(argv, " ")
		for _, scoping := range []string{
			"--package a", "--package b", "--minimum-test-timeout 180", "--timeout-multiplier 3",
			"--copy-target=false", "--jobs 1", "--sharding round-robin",
		} {
			if !strings.Contains(joined, scoping) {
				t.Errorf("shard %d argv = %v, want %q on every shard", i, argv, scoping)
			}
		}
		if tail := strings.Join(argv[len(argv)-3:], " "); tail != "-- -E "+filterset {
			t.Errorf("shard %d argv ends %q, want the passthrough %q last", i, tail, "-- -E "+filterset)
		}
		if got := argvValueOf(t, argv, "--shard"); got != fmt.Sprintf("%d/3", i) {
			t.Errorf("shard %d carries --shard %s, want %d/3: shard indexes are 0-based and k < n "+
				"(cargo-mutants 27.1.0 refuses 3/3 with \"shard k must be less than n\")", i, got, i)
		}
	}
	// Nothing else varies: two shards that differ anywhere but their own
	// number and their own output directory are measuring two different
	// things and their counts cannot be added up.
	for i := 1; i < len(argvs); i++ {
		normalised := strings.NewReplacer(
			fmt.Sprintf("--shard %d/3", i), "--shard 0/3",
			"/w/shard-"+strconv.Itoa(i), "/w/shard-0",
		).Replace(strings.Join(argvs[i], " "))
		if want := strings.Join(argvs[0], " "); normalised != want {
			t.Errorf("shard %d argv =\n  %s\ndiffers from shard 0 by more than its own number:\n  %s", i, normalised, want)
		}
	}
}

// The measurement that produced this design copied 375 GB per job — the
// workspace target dir, carried into every copy by `--copy-target=true` — took
// 1 h 47 min over the first copy, left 33 GB free so the other six never fit,
// and reached no verdict after 54 of 742 mutants. So the copy carries the
// SOURCE TREE ONLY and the warm build products live outside it: one
// cargo-mutants process per shard, each `--jobs 1`, each pointed at its own
// PERSISTENT target directory that outlives the run and makes the next one
// warm.
func TestMeasure_RunsOneProcessPerShardEachWithItsOwnPersistentTargetDir(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard}); err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 3 {
		t.Fatalf("ran the tool %d time(s), want one process per shard", len(*calls))
	}
	seen := map[string]bool{}
	for _, c := range *calls {
		shard := flagValue(c.Argv, "--shard")
		seen[shard] = true
		i, err := strconv.Atoi(strings.SplitN(shard, "/", 2)[0])
		if err != nil {
			t.Fatalf("--shard %q is not <i>/<n>", shard)
		}
		if got, want := flagValue(c.Argv, "--output"), mutantsShardDir(root, i); got != want {
			t.Errorf("shard %d wrote its outcomes to %q, want its own %q — one output dir shared by all "+
				"shards is one outcomes file overwritten N times", i, got, want)
		}
		target := mutantsShardTargetDir(root, i)
		if got := envValueOf(c.Env, "CARGO_TARGET_DIR"); got != target {
			t.Errorf("shard %d built in %q, want its own persistent %q", i, got, target)
		}
		if _, err := os.Stat(target); err != nil {
			t.Errorf("shard %d's target dir was not created before the run: %v", i, err)
		}
		if got, want := envValueOf(c.Env, "TMPDIR"), mutantsShardTempDir(root, i); got != want {
			t.Errorf("shard %d's TMPDIR = %q, want its own %q so the shards do not collide in temp", i, got, want)
		}
		if strings.Contains(strings.Join(c.Argv, " "), "--copy-target=true") {
			t.Errorf("shard %d argv = %v, want the SOURCE TREE only: the target dir is what made the copy 375 GB", i, c.Argv)
		}
	}
	if len(seen) != 3 {
		t.Errorf("shards measured = %v, want three distinct ones — a repeated shard measures the same "+
			"mutants twice and leaves another shard's unmeasured", seen)
	}
	// Persistent is the whole point: deleted at the end of the run, every
	// later run pays the cold build again.
	for i := range 3 {
		if _, err := os.Stat(mutantsShardTargetDir(root, i)); err != nil {
			t.Errorf("shard %d's target dir did not outlive the run: %v", i, err)
		}
	}
}

// The verdict is the MERGE of every shard's outcomes. A survivor that only
// shard 2 measured is still a survivor, and counts that stop at shard 0 are a
// report about a third of the lane.
func TestMeasure_ShardOutcomesAreMergedIntoOneVerdict(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	survivors := map[int]MutantOutcome{
		1: {File: "crates/a/src/lib.rs", Line: 2, Col: 5, Mutation: "replace * with /", Package: "a", Status: "missed"},
		2: {File: "crates/a/src/lib.rs", Line: 3, Col: 7, Mutation: "replace - with +", Package: "a", Status: "missed"},
	}
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		i := shardIndexOf(c.Argv)
		out := []MutantOutcome{{File: "crates/a/src/lib.rs", Line: 1, Col: 30 + i,
			Mutation: "replace + with -", Package: "a", Status: "caught"}}
		if m, ok := survivors[i]; ok {
			out = append(out, m)
		}
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), out...)
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Tested != 5 || v.Caught != 3 || v.Missed != 2 {
		t.Fatalf("verdict = %+v, want every shard's outcomes counted: 5 tested, 3 caught, 2 missed", v)
	}
	if !v.Refused {
		t.Fatalf("two unaccepted survivors must refuse the merge, got %+v", v)
	}
	for i, m := range survivors {
		if want := outcomeName(m); !strings.Contains(v.Message, want) {
			t.Errorf("message =\n%s\nwant shard %d's survivor %q named", v.Message, i, want)
		}
	}
}

// A shard that reached no verdict is not a shard with nothing to report. Its
// mutants were never measured, so the RUN reached no verdict — and the
// refusal has to say which shard and what it exited with, or an operator is
// left reading N logs to find the one that stopped.
func TestMeasure_ShardThatReachedNoVerdictRefusesNamingIt(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		if shardIndexOf(c.Argv) == 1 {
			// Died before it wrote anything: the shape of the run that
			// exited 1 having measured 54 of 742 mutants.
			return 1, nil
		}
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused || v.Tested != 0 {
		t.Fatalf("verdict = %+v, want a refusal that judged nothing — a third of the lane was never measured", v)
	}
	for _, want := range []string{"shard 1/3", "exited 1"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message = %q, want %q: the operator has to know which shard stopped", v.Message, want)
		}
	}
	if want := mutantsShardDir(root, 1); !strings.Contains(v.Message, want) {
		t.Errorf("message = %q, want that shard's own log under %q", v.Message, want)
	}
}

// Round-robin over N shards divides a pool that is not always bigger than N:
// five mutants over seven shards leaves two of them with nothing, and a
// cargo-mutants shard with nothing to test exits 0 having written no outcomes
// file at all — proved against the installed 27.1.0, which left `mutants.json`
// as `[]` and no `outcomes.json` beside it. Read as "this shard reached no
// verdict" that refuses every small lane on a many-core box, which is the
// first thing the real run did.
func TestMeasure_ShardWithNoMutantsOfItsOwnIsNotANoVerdict(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
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
	if v.Refused {
		t.Fatalf("verdict = %+v, want a pass: the third shard had no mutants to measure, which is not a shard that stopped", v)
	}
	if v.Tested != 2 || v.Caught != 2 {
		t.Errorf("verdict = %+v, want the two shards that had mutants counted", v)
	}
}

// The other side of that: a shard that was GIVEN mutants and reported none of
// them stopped part-way, whatever it exited with. Its mutants were never
// measured, and "no outcomes" must not read as "nothing survived".
func TestMeasure_ShardThatListedMutantsAndReportedNoneIsANoVerdict(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		out := flagValue(c.Argv, "--output")
		writeShardMutantList(t, out, 1)
		if shardIndexOf(c.Argv) == 1 {
			return 0, nil
		}
		writeOutcomesIn(t, out, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused || v.Tested != 0 {
		t.Fatalf("verdict = %+v, want a refusal that judged nothing: shard 1 was given a mutant and reported none", v)
	}
	if !strings.Contains(v.Message, "shard 1/2") {
		t.Errorf("message = %q, want the shard that stopped named", v.Message)
	}
}

// writeShardMutantList writes the list of mutants cargo-mutants gives one
// shard, which it writes before it tests any of them — the file that tells a
// shard with nothing to do apart from one that stopped.
func writeShardMutantList(t *testing.T, outDir string, mutants int) {
	t.Helper()
	entries := make([]string, mutants)
	for i := range entries {
		entries[i] = `{"name":"crates/a/src/lib.rs:1:36: replace + with -"}`
	}
	mustWrite(t, cargoMutantsListPath(outDir), "["+strings.Join(entries, ",")+"]")
}

// flagValue reads one flag's value out of an argv, "" when it is absent. It
// answers rather than failing, so a stubbed shard running on the run's own
// goroutine never calls t.Fatalf off the test's.
func flagValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// shardIndexOf is the i of a `--shard i/n`, -1 when the argv carries none.
func shardIndexOf(argv []string) int {
	i, err := strconv.Atoi(strings.SplitN(flagValue(argv, "--shard"), "/", 2)[0])
	if err != nil {
		return -1
	}
	return i
}
