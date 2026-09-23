package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func makeMeasureRepo(t *testing.T, lane map[string]string) (root, base string) {
	t.Helper()
	return tddtest.MakeMeasureRepo(t, setMutantsJobsForTest, lane)
}

// measureFixture is the common setup: a lane to measure, a state dir of its
// own, and a disk with room, so the test under it is about the step it names.
func measureFixture(t *testing.T, lane map[string]string) (root, base string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	return makeMeasureRepo(t, lane)
}

var laneSource = tddtest.LaneSource

// The measured diff is the lane's own changed CRATE SOURCES against the merge
// base — a test file is not mutated, and the crate set is what scopes both
// the mutant pool and the unmutated baseline (criterion 4).
//
// Both states have to give the same answer. At pre-merge-commit HEAD is still
// trunk and the merged tree exists only in the index and the worktree, so a
// selection taken against HEAD names nothing at all and the stage would pass
// on "nothing to measure" — while the diff FILE handed to the runner was
// rendered against the worktree all along. Selection and content are one
// diff, base against the worktree, in both states.
func TestMeasureDiff_ScopesToCrateSourcesAtMergeBase(t *testing.T) {
	lane := map[string]string{
		"crates/a/src/lib.rs": "pub fn add(a: i32, b: i32) -> i32 { a + b + 0 }\n",
		"crates/a/tests/t.rs": "#[test]\nfn t() { assert_eq!(1, 1); }\n",
	}
	for _, tc := range []struct {
		name  string
		build func(*testing.T, map[string]string) (string, string)
	}{
		{"lane checkout", makeMeasureRepo},
		{"merge staged in the index and worktree", makeStagedMergeRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			root, base := tc.build(t, lane)

			files, crates, err := measureDiff(root, base)

			if err != nil {
				t.Fatalf("measureDiff: %v", err)
			}
			if len(files) != 1 || files[0] != "crates/a/src/lib.rs" {
				t.Fatalf("files = %v, want only the changed crate source", files)
			}
			if len(crates) != 1 || crates[0] != "a" {
				t.Errorf("crates = %v, want the one crate owning it", crates)
			}
			path, err := writeMeasureDiff(root, base, files)
			if err != nil {
				t.Fatalf("writeMeasureDiff: %v", err)
			}
			patch, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("no diff written for the runner: %v", readErr)
			}
			if !strings.Contains(string(patch), "a + b + 0") {
				t.Errorf("diff file =\n%s\nwant the changed line's own hunk", patch)
			}
		})
	}
}

// A lane that changed no mutable source has nothing to measure and must not
// spend a run finding that out (criterion 4).
func TestMeasureDiff_NoMutableSourcePassesWithoutRunning(t *testing.T) {
	root, base := measureFixture(t, map[string]string{"README.md": "lane\n"})
	calls := stubMutantsExec(t, nil)

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Skipped != "nothing to measure (0 changed source files)" {
		t.Errorf("Skipped = %q, want the reason nothing ran", v.Skipped)
	}
	if v.Refused {
		t.Errorf("a lane with nothing to measure must pass, got %+v", v)
	}
	if len(*calls) != 0 {
		t.Errorf("ran the mutation tool %d time(s) for a lane with no mutable source: %+v", len(*calls), *calls)
	}
}

// The whole argv in one place, so what cargo-mutants is asked to do is one
// literal a reviewer can read (criterion 5).
func TestMutantsArgv_ExactForACargoLane(t *testing.T) {
	t.Parallel()
	got := MutantsArgv("/w/changed.diff", 120, []string{"a"}, "not(test(slow))")

	want := []string{
		"--copy-target=false", "--in-diff", "/w/changed.diff", "--no-shuffle", "--test-tool=nextest",
		"--minimum-test-timeout", "120", "--timeout-multiplier", "3",
		"--package", "a", "--", "-E", "not(test(slow))",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("MutantsArgv =\n  %v\nwant\n  %v", got, want)
	}
}

// The cap is printed as well as applied: a run that measured under two jobs
// on a 24-core box has to say why, or the number reads as a bug (criterion 6).
func TestJobsCap_MinOfCoresRamAndTwo(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cores, ramGB, jobs int
		why                string
	}{
		{24, 64, 8, "min(cores 24/3=8, ram 64GB/8=8, cap 8) — cap 8"},
		{6, 64, 2, "min(cores 6/3=2, ram 64GB/8=8, cap 8) — cores"},
		{24, 0, 8, "min(cores 24/3=8, ram unknown, cap 8) — cap 8"},
		{24, 6, 1, "min(cores 24/3=8, ram 6GB/8=0, cap 8) — ram"},
	} {
		// 0 free: unreadable, so these are the RAM term's own cases.
		jobs, why := MutantsJobsCap(c.cores, c.ramGB, 0)
		if jobs != c.jobs || why != c.why {
			t.Errorf("MutantsJobsCap(%d, %d) = (%d, %q), want (%d, %q)", c.cores, c.ramGB, jobs, why, c.jobs, c.why)
		}
	}
}

// A run that cannot fit its temp copies fills the drive and dies mid-way,
// taking every verdict it had reached with it. It is refused BEFORE it starts,
// with both numbers, and the refusal is logged so `gate stats` can count it
// (criterion 7).
func TestDiskCheck_RefusesNamingBothNumbers(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	// A drive with 10 GB free cannot carry even ONE shard — a cold build dir
	// alone is estimated at 15 GB, and 10 GB is kept free besides — so there
	// is nothing to reduce to and the run is refused. The shard count is
	// pinned, not read from the box, or the message would name whatever
	// machine runs the test.
	t.Cleanup(SetFreeSpaceForTest(10, true))
	root, base := makeMeasureRepo(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	calls := stubMutantsExec(t, nil)

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("a run that cannot fit must be refused, got %+v", v)
	}
	// expectation-changed: the budget is per SHARD and measured, so the
	// shortfall is stated against one shard's own need rather than against a
	// flat per-job constant times the job count.
	for _, want := range []string{"10 GB free", "one shard needs", "15.0 GB build dir"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message = %q, want %q in it", v.Message, want)
		}
	}
	if strings.Contains(v.Message, "jobs=") {
		t.Errorf("message = %q, want the run's own vocabulary: it starts shards, not jobs", v.Message)
	}
	if len(*calls) != 0 {
		t.Errorf("started the tool anyway: %+v", *calls)
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-refused:disk") {
		t.Errorf("gate.log has no mutants-refused:disk line:\n%s", gateLogText(t, cfgDir))
	}
}

// TMPDIR alone is what a Windows cargo-mutants ignores: it wrote its tree
// copies to C: until the drive was 98% full. All three names, always, plus
// the switches the repo declares and the nextest profile it declares
// (criteria 7 and 8).
func TestMeasureEnv_SetsAllThreeTempNamesAndProfile(t *testing.T) {
	root := t.TempDir()
	cfg := MutantsConfig{Env: []string{"BORLD_GPU=1"}}

	env := measureEnv(root, cfg)

	wantTemp := measureTempDir(root)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if got := envValueOf(env, name); got != wantTemp {
			t.Errorf("%s = %q, want %q", name, got, wantTemp)
		}
	}
	if got := envValueOf(env, "BORLD_GPU"); got != "1" {
		t.Errorf("BORLD_GPU = %q, want the declared switch exported", got)
	}
	// The children of this run reach cargo through the queue shim, which
	// refuses a bare `cargo mutants` and queues every other build behind the
	// editors on the box. This marker is what tells the shim the call is the
	// gate's own: without it the run the gate just started is refused by the
	// shim it started it through.
	if got := envValueOf(env, MutationGateEnv); got != MutationGateMarked {
		t.Errorf("%s = %q, want %q so the cargo shim lets the gate's own run through", MutationGateEnv, got, MutationGateMarked)
	}
	if got := envValueOf(env, "NEXTEST_PROFILE"); got != "" {
		t.Errorf("NEXTEST_PROFILE = %q, want none until the repo declares [profile.mutants]", got)
	}

	mustWrite(t, filepath.Join(root, ".config", "nextest.toml"), "[profile.mutants]\nslow-timeout = \"120s\"\n")

	if got := envValueOf(measureEnv(root, cfg), "NEXTEST_PROFILE"); got != "mutants" {
		t.Errorf("NEXTEST_PROFILE = %q, want the declared profile", got)
	}
}

// envValueOf reads one variable out of a rendered child environment.
func envValueOf(env []string, name string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			value = v
		}
	}
	return value
}

// Contention must not read as a timeout: the budget is three times what the
// unmutated suite actually took, never under two minutes, and each run
// records what it measured for the next one (criterion 5).
func TestMinTestTimeout_ThreeTimesLastBaselineFlooredAt120(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()

	if got := mutantsMinTestTimeout(root); got != 120 {
		t.Errorf("with no recorded baseline = %d, want the 120 s floor", got)
	}
	writeMutantsBaselineSeconds(root, 50)
	if got := mutantsMinTestTimeout(root); got != 150 {
		t.Errorf("with a 50 s baseline = %d, want 3x it", got)
	}
	writeMutantsBaselineSeconds(root, 30)
	if got := mutantsMinTestTimeout(root); got != 120 {
		t.Errorf("with a 30 s baseline = %d, want the floor to win", got)
	}

	recordMutantsBaseline(root, "     ok       Unmutated baseline in 12.5s build + 30.0s test\n")

	if got := readMutantsBaselineSecondsText(root); got != "42.5" {
		t.Errorf("recorded baseline = %q, want the run's own build + test seconds", got)
	}
}
