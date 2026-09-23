package tdd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

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
	t.Cleanup(SetMutantsGOOSForTest("windows"))
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
	t.Cleanup(SetMutantsGOOSForTest("linux"))
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

func makeGoMeasureRepo(t *testing.T) (root, base string) {
	t.Helper()
	return tddtest.MakeGoMeasureRepo(t)
}

func writeMeasureBase(t *testing.T, root string) { t.Helper(); tddtest.WriteMeasureBase(t, root) }

func readFileString(t *testing.T, path string) string {
	t.Helper()
	return tddtest.ReadFileString(t, path)
}
