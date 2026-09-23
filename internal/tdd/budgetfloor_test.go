package tdd

import (
	"os"
	"strings"
	"testing"
	"time"
)

// budgetFloorFixtureLeaf is this file's own synthetic process-tree leaf, in
// the same above-anything-an-OS-assigns space machineload_test.go's fixtures
// use (see maxAssignableOSPID there, and PR #640): a chain rooted inside the
// assignable range can BE the process running the test, which costs the
// report the foreign leaf it is asserting on.
const budgetFloorFixtureLeaf = syntheticPIDBase + 7

// TestRunCargoLocked_BudgetUnderLoadNeverFallsBelowTheRecordedFloor is issue
// #660. The stage budget is carved into by however long this run queued for
// a build slot, so a busy box handed the SAME suite 441s, 96s, 148s and 335s
// on four attempts at a suite that needs ~350s to finish — three of those
// four could never have passed, and each refusal cost a full run and proved
// nothing. The box's load is the right input for deciding whether to START
// work and the wrong one for deciding how long the work TAKES: whatever the
// wait left, the run must still get at least the time this suite is recorded
// to need.
func TestRunCargoLocked_BudgetUnderLoadNeverFallsBelowTheRecordedFloor(t *testing.T) {
	withIsolatedBuildLock(t)

	// Simulated heavy load: the slot this run needs is already held, so the
	// acquisition really waits and the wait really eats the stage budget —
	// the same arithmetic that turned 600s into 96s.
	const holdFor = 300 * time.Millisecond
	root := t.TempDir()
	_, release, ok := acquireBuildSlot(resolveTargetDir(os.Getenv, root), time.Second, "cargo nextest run -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the build slot")
	}
	go func() {
		// real-time: the OS file lock has no observable hand-off event
		time.Sleep(holdFor)
		release()
	}()

	var gotDeadline time.Time
	stub := func(r Runner, _ string) SuiteResult {
		gotDeadline = r.Deadline
		return SuiteResult{Passed: true}
	}

	// The wait above eats all but ~50ms of the stage budget; floor is what
	// this suite's own record says the work takes.
	const stageBudget = 350 * time.Millisecond
	const floor = 250 * time.Millisecond
	res, waited, acquired := runCargoLocked(stub, Runner{Cmd: "cargo"}, root, time.Second, stageBudget, floor)
	if !acquired || !res.Passed {
		t.Fatalf("expected the slot to be acquired once released, acquired=%v res=%+v", acquired, res)
	}
	if waited < 200*time.Millisecond {
		t.Fatalf("setup: the acquirer never actually waited behind the held slot, waited=%s", waited)
	}
	if gotDeadline.IsZero() {
		t.Fatal("the stub never saw a Runner.Deadline at all")
	}
	// Tolerance for scheduling jitter across the goroutine and the polling
	// loop, far smaller than the ~200ms gap between a floored budget and the
	// ~50ms the wait would otherwise have left.
	const tolerance = 40 * time.Millisecond
	if remaining := time.Until(gotDeadline); remaining < floor-tolerance {
		t.Fatalf("the suite was handed %s of budget against a %s recorded floor — the box's load decided how long the work takes, which is issue #660", remaining, floor)
	}
}

// TestSuiteFloorFrom_TakesTheSlowTailNotTheMedian pins the statistic. These
// are the five durations this repo's own suite recorded on the night #660
// was filed. The median (357.0s) is blind to exactly the slow tail that
// makes a budget bite, and a bare max is one outlier away from being
// somebody else's bad night; the nearest-rank p90 tracks the tail while
// needing more than one slow run to move.
func TestSuiteFloorFrom_TakesTheSlowTailNotTheMedian(t *testing.T) {
	got := suiteFloorFrom([]float64{352.3, 357.0, 429.1, 431.3, 261.5})

	if got.Runs != 5 {
		t.Fatalf("runs = %d, want the 5 recorded completions", got.Runs)
	}
	if got.StatSecs != 431.3 {
		t.Fatalf("statSecs = %v, want the p90 431.3 (the median 357.0 is what ignores the slow tail)", got.StatSecs)
	}
	// 431.3s plus the margin, rounded to whole seconds.
	if want := 647 * time.Second; got.Budget != want {
		t.Fatalf("budget = %s, want %s (p90 × the %.2g margin)", got.Budget, want, suiteFloorMargin)
	}
}

// TestRecordedSuiteFloor_CountsOnlyCompletedRunsOfThisStageAndCommand is the
// evidence rule: gate.log already records every run's stage, command and
// duration, and only a run that FINISHED measured the work. A timeout's
// duration is the budget it was cut off at, a cache hit measured nothing,
// and another stage's (or another command's) run is not this suite.
func TestRecordedSuiteFloor_CountsOnlyCompletedRunsOfThisStageAndCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	const cmd = "cargo nextest run -p borld-core"

	for _, secs := range []float64{352.3, 357.0, 429.1, 431.3, 261.5} {
		AppendGateLog(premergeDisplayName, root, cmd, "green", time.Duration(secs*float64(time.Second)))
	}
	AppendGateLog(premergeDisplayName, root, cmd, "timeout-rejected", 441*time.Second)
	AppendGateLog(premergeDisplayName, root, cmd, "cache-hit", 0)
	AppendGateLog("postedit", root, cmd, "green", 12*time.Second)
	AppendGateLog(premergeDisplayName, root, "cargo nextest run -p other-crate", "green", 900*time.Second)

	got := recordedSuiteFloor(premergeDisplayName, cmd)

	if got.Runs != 5 {
		t.Fatalf("runs = %d, want only the 5 completed runs of this stage and command", got.Runs)
	}
	if got.StatSecs != 431.3 {
		t.Fatalf("statSecs = %v, want 431.3 — a timeout, a cache hit, another stage or another command must not reach the statistic", got.StatSecs)
	}
}

// TestRecordedSuiteFloor_NoRecordedRunMeansNoFloorAtAll is the
// no-record-no-regression rule: a repo or a suite with nothing in gate.log
// yet keeps working exactly as it does today. A floor derived from evidence
// that does not exist would make a first run impossible to have.
func TestRecordedSuiteFloor_NoRecordedRunMeansNoFloorAtAll(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	got := recordedSuiteFloor(premergeDisplayName, "cargo nextest run -p never-run")

	if got.Budget != 0 || got.Runs != 0 {
		t.Fatalf("floor = %+v, want a zero floor when gate.log has no completed run to derive one from", got)
	}
	if note := got.RefusalNote(DefaultPrecommitTimeout); !strings.Contains(note, "no completed run") {
		t.Fatalf("refusal note = %q, want it to say there is no recorded run to floor the budget at", note)
	}
}

// TestSuiteFloorFrom_NeverOutgrowsTheStageBudget keeps a hang killable: the
// floor raises a budget the load shrank, it never removes the ceiling. A
// suite recorded at 590s asks for 885s, and what the run may actually have
// is still the stage budget.
func TestSuiteFloorFrom_NeverOutgrowsTheStageBudget(t *testing.T) {
	got := suiteFloorFrom([]float64{590})

	if got.Budget <= DefaultPrecommitTimeout {
		t.Fatalf("setup: budget = %s, this test needs a floor that asks for MORE than the %s stage budget", got.Budget, DefaultPrecommitTimeout)
	}
	if capped := cappedFloor(got.Budget, DefaultPrecommitTimeout); capped != DefaultPrecommitTimeout {
		t.Fatalf("capped floor = %s, want the %s stage budget — a suite that genuinely hangs must still be cut off", capped, DefaultPrecommitTimeout)
	}
	if note := got.RefusalNote(DefaultPrecommitTimeout); !strings.Contains(note, "capped") {
		t.Fatalf("refusal note = %q, want it to say the floor was capped at the stage budget", note)
	}
}

// TestMechanical_TimeoutRefusalNamesTheFloorItUsedAndWhereItCameFrom is
// #660's other half: the old refusal promised "The gate target is now warm;
// retry the commit", which under sustained load promises an improvement the
// next (smaller) budget cannot deliver. The refusal now says which floor the
// run got and what evidence set it.
func TestMechanical_TimeoutRefusalNamesTheFloorItUsedAndWhereItCameFrom(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	t.Cleanup(SetMachineLoadSampleForTest(func(<-chan struct{}) (int, float64, []procSample, bool) {
		return 4, 97, foreignChainSample(budgetFloorFixtureLeaf, "rustc.exe", 95, 3), true
	}))

	// Run one records what this suite takes: a run that FAILED still ran to
	// completion, so its duration is evidence about the work.
	red := func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		return SuiteResult{Passed: false, Output: "--- FAIL: TestWidget_isOne\nFAIL\n", Duration: 300 * time.Second}
	}
	if res := Mechanical(root, red); !res.Blocked {
		t.Fatal("setup: a failing suite must block, so gate.log records its duration")
	}

	// Run two, on the same tree — a red run is never cached, so the suite
	// really runs again — is killed by the budget.
	timedOut := func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		return SuiteResult{Passed: false, TimedOut: true, Duration: 90 * time.Second}
	}
	res := Mechanical(root, timedOut)

	if !res.Blocked {
		t.Fatal("a merge whose suite never finished must still be refused")
	}
	for _, want := range []string{"450s", "300.0s", "gate.log", "box: 4 cores, load 97%"} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("message = %q, want it to name the floor it used (450s), the record it came from (300.0s in gate.log) and the box load", res.Message)
		}
	}
	if strings.Contains(res.Message, "now warm") {
		t.Fatalf("message = %q, want it to stop promising a warm retry the next budget cannot deliver", res.Message)
	}
}
