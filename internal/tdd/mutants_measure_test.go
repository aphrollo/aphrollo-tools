package tdd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeMeasureRepo builds a one-crate cargo workspace with a base commit and a
// lane commit on top of it, and answers the root and the base sha. The shard
// count is pinned to ONE so a test built on it is about the step it names
// rather than about how many cores the box running the suite has; a test
// about the sharding pins its own number after this call.
func makeMeasureRepo(t *testing.T, lane map[string]string) (root, base string) {
	t.Helper()
	t.Cleanup(setMutantsJobsForTest(1, "pinned"))
	root = t.TempDir()
	gitInit(t, root)
	writeMeasureBase(t, root)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	base = strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD"))
	for rel, content := range lane {
		write(t, root, rel, content)
	}
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	return root, base
}

// measureFixture is the common setup: a lane to measure, a state dir of its
// own, and a disk with room, so the test under it is about the step it names.
func measureFixture(t *testing.T, lane map[string]string) (root, base string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	return makeMeasureRepo(t, lane)
}

// laneSource is the lane commit every judgement test makes: one changed crate
// source, so there is something mutable to measure.
var laneSource = map[string]string{"crates/a/src/lib.rs": "pub fn add(a: i32, b: i32) -> i32 { a - b }\n"}

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

// An exit status cargo-mutants does not use for a verdict means the run
// reached none. It refuses, and says where the log that explains it is —
// never "0 missed" from a file that was never written (criterion 9).
func TestMeasure_NoVerdictExitRefusesWithStatusAndLog(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) { return 1, nil })

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("an exit with no verdict must be refused, got %+v", v)
	}
	if !strings.Contains(v.Message, "exited 1") {
		t.Errorf("message = %q, want the status named", v.Message)
	}
	if want := cargoMutantsLogDir(mutantsShardDir(root, 0)); !strings.Contains(v.Message, want) {
		t.Errorf("message = %q, want the failing shard's own log path %q", v.Message, want)
	}
}

// An outcomes file an EARLIER run left behind describes a different tree. A
// run that writes none reached no verdict, and reading the stale one would
// let a lane merge on somebody else's measurement (criterion 9).
func TestMeasure_StaleOutcomesFromAnEarlierRunAreNeverJudged(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 9, Col: 1,
		Mutation: "replace + with -", Package: "a", Status: "caught"})
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) { return 0, nil })

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused || v.Tested != 0 {
		t.Fatalf("verdict = %+v, want a refusal that judged nothing — the outcomes are an older run's", v)
	}
}

// Nine timeouts on one lane were all contention, and a refusal that names
// contention as a survivor is a false report. So a timed-out mutant is
// re-run once, alone; one that times out again is unmeasured, and an
// unmeasured mutant is not a caught one (criterion 10).
func TestMeasure_TimedOutMutantRerunsOnceAloneThenRefuses(t *testing.T) {
	slow := MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "timeout"}

	for _, tc := range []struct {
		name       string
		second     string
		refused    bool
		unmeasured int
	}{
		{"times out again", "timeout", true, 1},
		{"caught when alone", "caught", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, base := measureFixture(t, laneSource)
			calls := stubMutantsExec(t, func(_ context.Context, n int, c measuredCall) (int, error) {
				m := slow
				if n == 2 {
					m.Status = tc.second
				}
				writeOutcomes(t, root, m)
				return 0, nil
			})

			v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

			if err != nil {
				t.Fatalf("MeasureLane: %v", err)
			}
			if len(*calls) != 2 {
				t.Fatalf("ran the tool %d time(s), want the run plus one lone re-run", len(*calls))
			}
			// The re-run is alone on the box by construction — an in-place
			// run is one job and cannot be told otherwise (#592) — so what
			// is asserted here is the filter that makes it about this
			// mutant; the whole argv is pinned by
			// TestMutantsRerun_KeepsInPlaceAndAddsOnlyTheNameFilter.
			rerun := strings.Join((*calls)[1].Argv, " ")
			if !strings.Contains(rerun, `--re ^crates/a/src/lib\.rs:1:36: replace \+ with -$`) {
				t.Errorf("re-run argv = %q, want a name filter for the timed-out mutant alone", rerun)
			}
			if v.Refused != tc.refused {
				t.Errorf("Refused = %v, want %v: %s", v.Refused, tc.refused, v.Message)
			}
			if len(v.Unmeasured) != tc.unmeasured {
				t.Errorf("Unmeasured = %+v, want %d", v.Unmeasured, tc.unmeasured)
			}
		})
	}
}

// The mutant comes FIRST, because it is the finding; the counts and the
// remedy follow it. The receipt stage this replaces refused 150 merges and
// named a survivor in none of them (criterion 12).
func TestJudge_UnacceptedMissedRefusesNamingMutantFirst(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	accepted := MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "missed"}
	survivor := MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 40, Mutation: "replace add -> i32 with 0", Package: "a", Status: "missed"}
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		writeOutcomes(t, root, accepted, survivor,
			MutantOutcome{File: "crates/a/src/lib.rs", Line: 2, Col: 5, Mutation: "replace * with /", Package: "a", Status: "caught"},
			MutantOutcome{File: "crates/a/src/lib.rs", Line: 3, Col: 5, Mutation: "replace / with %", Package: "a", Status: "caught"})
		return 0, nil
	})
	cfg := MutantsConfig{AtMerge: true, Accept: []string{
		"crates/a/src/lib.rs:1:36 replace + with - # kind=equivalent: addition is commutative here",
	}}

	v, err := MeasureLane(root, cfg, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("an unaccepted survivor must refuse the merge, got %+v", v)
	}
	lines := strings.Split(strings.TrimSpace(v.Message), "\n")
	wantFirst := mutantLineOf(survivor.File, survivor.Line, survivor.Col, survivor.Mutation)
	if lines[0] != wantFirst {
		t.Errorf("first line = %q, want the unaccepted mutant %q", lines[0], wantFirst)
	}
	wantSummary := "mutants: 4 tested, 2 caught, 0 unviable, 2 missed (1 accepted), 0 unmeasured"
	if len(lines) < 3 || lines[1] != wantSummary {
		t.Fatalf("message =\n%s\nwant the summary line %q second", v.Message, wantSummary)
	}
	if !strings.Contains(lines[len(lines)-1], "mutation-accept") {
		t.Errorf("last line = %q, want the remedy naming mutation-accept", lines[len(lines)-1])
	}
}

// An accept-list nobody had to justify is a list of survivors somebody
// silenced. A malformed entry is refused by name, never read as an ordinary
// equivalence claim (criterion 13).
func TestJudge_MalformedAcceptEntryIsARefusalNamingIt(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	entry := "crates/a/src/lib.rs:3:5: replace x with y # kind=bogus: why"

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true, Accept: []string{entry}}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("a malformed accept entry must refuse, got %+v", v)
	}
	if !strings.Contains(v.Message, entry) {
		t.Errorf("message = %q, want the entry quoted", v.Message)
	}
}

// The binary must not know what a test's side effects are. mutants-after is
// the one hook a repo gets to reclaim what a timeout-killed test binary left
// behind — it is told the verdict and can never change it (criterion 14).
func TestAfterHook_RunsWithStatusAndNeverChangesVerdict(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed") // skip-ok: mutants-after is a shell script and a box without bash cannot run one
	}
	for _, tc := range []struct {
		name, status string
		status0      string
		refused      bool
	}{
		{name: "refused run", status: "1", status0: "missed", refused: true},
		{name: "passed run", status: "0", status0: "caught", refused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, base := measureFixture(t, laneSource)
			mustWrite(t, filepath.Join(root, "tools", "after.sh"),
				"#!/bin/sh\nprintf '%s' \"$APHROLLO_MUTANTS_STATUS\" > after-status.txt\nexit 3\n")
			stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
				writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
					Mutation: "replace + with -", Package: "a", Status: tc.status0})
				return 0, nil
			})
			var log bytes.Buffer

			v, err := MeasureLane(root, MutantsConfig{AtMerge: true, After: "tools/after.sh"},
				MeasureOpts{Base: base, Log: &log})

			if err != nil {
				t.Fatalf("MeasureLane: %v", err)
			}
			if v.Refused != tc.refused {
				t.Fatalf("Refused = %v, want %v — the hook must not change the verdict: %s", v.Refused, tc.refused, v.Message)
			}
			got, readErr := os.ReadFile(filepath.Join(root, "after-status.txt"))
			if readErr != nil {
				t.Fatalf("mutants-after did not run in the worktree: %v", readErr)
			}
			if string(got) != tc.status {
				t.Errorf("APHROLLO_MUTANTS_STATUS = %q, want %q", got, tc.status)
			}
			if !strings.Contains(log.String(), "mutants-after exited 3") {
				t.Errorf("log = %q, want the hook's own failure recorded", log.String())
			}
		})
	}
}

// gremlins reports 0.00% mutator coverage on Windows: 4890 mutants NOT
// COVERED. A receipt saying every mutant survived is the wrong answer in the
// blocking direction, so the stage stands down and says so (criterion 11).
func TestMeasure_GoRepoOnWindowsStandsDown(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(setMutantsGOOSForTest("windows"))
	calls := stubMutantsExec(t, nil)

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.NotMeasured == "" {
		t.Errorf("NotMeasured = %q, want the reason the run could not happen", v.NotMeasured)
	}
	if v.Refused {
		t.Errorf("standing down must pass, got %+v", v)
	}
	if len(*calls) != 0 {
		t.Errorf("ran gremlins anyway: %+v", *calls)
	}
	if !strings.Contains(gateLogText(t, cfgDir), "mutants-unmeasured:gremlins-windows") {
		t.Errorf("gate.log has no stand-down line:\n%s", gateLogText(t, cfgDir))
	}
}

// A Go repo is measured by the same runner the detached job used, scoped to
// the same merge base, and its report is read the same way (criterion 11).
func TestMeasure_GoRepoUsesGremlinsScopedToMergeBase(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetFreeSpaceForTest(999, true))
	root, base := makeGoMeasureRepo(t)
	t.Cleanup(setMutantsGOOSForTest("linux"))
	// The worker count is the BOX's, and the argv below is asserted exactly:
	// pinned here, this test is about what gremlins is asked to do rather
	// than how many cores the machine running the suite has. A CI runner
	// derives one and a developer box two, and the argv is the same
	// otherwise.
	t.Cleanup(setMutantsJobsForTest(2, "min(cores 24/6=4, ram 64GB/6=10, cap 2) — cap 2"))
	calls := stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		mustWrite(t, gremlinsReportPath(root), `{"files":[{"file_name":"calc.go","mutations":[
			{"type":"ARITHMETIC_BASE","status":"KILLED","line":3,"column":20}]}]}`)
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("ran the tool %d time(s), want one gremlins run", len(*calls))
	}
	want := append([]string{gremlinsBin}, gremlinsArgv(base, gremlinsReportPath(root), 2, nil)...)
	if strings.Join((*calls)[0].Argv, " ") != strings.Join(want, " ") {
		t.Errorf("argv =\n  %v\nwant\n  %v", (*calls)[0].Argv, want)
	}
	if v.Refused || v.Caught != 1 {
		t.Errorf("verdict = %+v, want the one caught mutant read out of the report", v)
	}
}

// makeGoMeasureRepo builds a Go module with a base commit and a lane commit
// that changes a source file.
func makeGoMeasureRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = makeGoRepo(t)
	base = strings.TrimSpace(gitOutT(t, root, "rev-parse", "HEAD"))
	write(t, root, "calc.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	return root, base
}

// writeMeasureBase lays down the one-crate workspace both fixtures start
// from: a workspace manifest, a crate with a mutable source and a test, and
// a file that is neither.
func writeMeasureBase(t *testing.T, root string) {
	t.Helper()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	write(t, root, "crates/a/Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b }\n")
	write(t, root, "crates/a/tests/t.rs", "#[test]\nfn t() {}\n")
	write(t, root, "README.md", "base\n")
}

// readFileString reads a file the run may have rewritten.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
