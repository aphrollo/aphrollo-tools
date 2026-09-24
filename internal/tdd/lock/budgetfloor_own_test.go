package lock

import (
	"strings"
	"testing"
	"time"
)

// TestCappedFloor_NeverExceedsTheStageBudget pins the one place a hang stays
// killable: a floor derived from a slow record may ask for more than the
// stage budget, but a run may never actually get more than that ceiling.
func TestCappedFloor_NeverExceedsTheStageBudget(t *testing.T) {
	if got := cappedFloor(900*time.Second, 350*time.Second); got != 350*time.Second {
		t.Fatalf("cappedFloor(900s, 350s) = %s, want the 350s stage budget", got)
	}
}

// TestCappedFloor_PassesThroughAFloorUnderTheBudget is the other half: a
// floor that asks for less than the stage budget is granted exactly what it
// asks for, never rounded up to the ceiling.
func TestCappedFloor_PassesThroughAFloorUnderTheBudget(t *testing.T) {
	if got := cappedFloor(200*time.Second, 350*time.Second); got != 200*time.Second {
		t.Fatalf("cappedFloor(200s, 350s) = %s, want the 200s floor unchanged", got)
	}
}

// TestSuiteFloor_RefusalNote_NoRecordSaysSo is the no-record-no-floor rule:
// with nothing derived, the note names the configured stage budget rather
// than claiming a floor that evidence never set.
func TestSuiteFloor_RefusalNote_NoRecordSaysSo(t *testing.T) {
	var f suiteFloor
	note := f.RefusalNote(350 * time.Second)
	if !strings.Contains(note, "floor: none") || !strings.Contains(note, "350s") {
		t.Fatalf("note = %q, want it to say there is no floor and name the 350s stage budget", note)
	}
}

// TestSuiteFloor_RefusalNote_NamesTheRecordBehindTheFloor is #660's ask: the
// refusal must name the floor it used and the evidence it came from, not the
// old "now warm, retry" line that promised an improvement a smaller budget
// under load could not deliver.
func TestSuiteFloor_RefusalNote_NamesTheRecordBehindTheFloor(t *testing.T) {
	f := suiteFloor{Budget: 300 * time.Second, StatSecs: 200, Runs: 4}
	note := f.RefusalNote(500 * time.Second)
	for _, want := range []string{"floor: 300s", "200.0s", "4 completed run", "gate.log"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note = %q, want it to contain %q", note, want)
		}
	}
	if strings.Contains(note, "now warm") {
		t.Fatalf("note = %q, must not promise a warm retry the next budget cannot deliver", note)
	}
}

// TestSuiteFloor_RefusalNote_SaysWhenTheFloorWasCapped covers the other
// branch of the same message: a floor that exceeds the stage budget must say
// it was capped, so a reader is not left thinking the run got the full
// (uncapped) floor.
func TestSuiteFloor_RefusalNote_SaysWhenTheFloorWasCapped(t *testing.T) {
	f := suiteFloor{Budget: 900 * time.Second, StatSecs: 600, Runs: 1}
	note := f.RefusalNote(350 * time.Second)
	if !strings.Contains(note, "capped at the 350s stage budget") {
		t.Fatalf("note = %q, want it to say the floor was capped at the 350s stage budget", note)
	}
}

// TestSuiteFloorFrom_TakesTheP90NotTheMedian pins the statistic against the
// same five durations issue #660 was filed with: the median (357.0s) is
// blind to exactly the slow tail that makes a budget bite, so the floor is
// derived from the nearest-rank p90 instead.
func TestSuiteFloorFrom_TakesTheP90NotTheMedian(t *testing.T) {
	got := suiteFloorFrom([]float64{352.3, 357.0, 429.1, 431.3, 261.5})

	if got.Runs != 5 {
		t.Fatalf("runs = %d, want the 5 samples", got.Runs)
	}
	if got.StatSecs != 431.3 {
		t.Fatalf("statSecs = %v, want the p90 431.3 (357.0 is the median the stat must NOT take)", got.StatSecs)
	}
	if want := 647 * time.Second; got.Budget != want {
		t.Fatalf("budget = %s, want %s (431.3 * the %.2g margin, rounded)", got.Budget, want, suiteFloorMargin)
	}
}

// TestSuiteFloorFrom_EmptyMeansNoFloor is the no-record rule at the
// statistic's own level: nothing to derive from is a zero suiteFloor, never
// a guess.
func TestSuiteFloorFrom_EmptyMeansNoFloor(t *testing.T) {
	got := suiteFloorFrom(nil)
	if got != (suiteFloor{}) {
		t.Fatalf("suiteFloorFrom(nil) = %+v, want the zero value", got)
	}
}

// TestNearestRankIndex_ClampsAndRounds pins the nearest-rank definition
// (ceil(p*n)-1, clamped into range) against hand-checked indices rather than
// re-deriving the formula: p90 of 5 samples is index 4 (the maximum), the
// median (p50) of 5 is index 2, and an n of 1 always answers index 0.
func TestNearestRankIndex_ClampsAndRounds(t *testing.T) {
	cases := []struct {
		n    int
		p    float64
		want int
	}{
		{n: 5, p: 0.9, want: 4},
		{n: 5, p: 0.5, want: 2},
		{n: 1, p: 0.9, want: 0},
		{n: 10, p: 0.1, want: 0},
	}
	for _, c := range cases {
		if got := nearestRankIndex(c.n, c.p); got != c.want {
			t.Errorf("nearestRankIndex(%d, %v) = %d, want %d", c.n, c.p, got, c.want)
		}
	}
}

// TestRecordedSuiteFloor_CountsOnlyCompletedRunsOfThisStageAndCommandDirectly is the
// evidence rule at the gate.log layer: only a run that FINISHED measured the
// work, and only this stage's and this command's runs are this suite's own
// record. A timeout's duration is the budget it was cut off at, a cache hit
// measured nothing, and another stage's or command's run is not this suite.
func TestRecordedSuiteFloor_CountsOnlyCompletedRunsOfThisStageAndCommandDirectly(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	const stage = "premerge"
	const cmd = "cargo nextest run -p borld-core"

	for _, secs := range []float64{352.3, 357.0, 429.1, 431.3, 261.5} {
		AppendGateLog(stage, root, cmd, "green", time.Duration(secs*float64(time.Second)))
	}
	AppendGateLog(stage, root, cmd, "timeout-rejected", 441*time.Second)
	AppendGateLog(stage, root, cmd, "cache-hit", 0)
	AppendGateLog("postedit", root, cmd, "green", 12*time.Second)
	AppendGateLog(stage, root, "cargo nextest run -p other-crate", "green", 900*time.Second)

	got := recordedSuiteFloor(stage, cmd)

	if got.Runs != 5 {
		t.Fatalf("runs = %d, want only the 5 completed runs of this stage and command", got.Runs)
	}
	if got.StatSecs != 431.3 {
		t.Fatalf("statSecs = %v, want 431.3 — a timeout, a cache hit, another stage or another command must not reach the statistic", got.StatSecs)
	}
}

// TestRecordedSuiteFloor_NoRecordedRunMeansNoFloorAtAllDirectly is the
// no-record-no-regression rule: a repo or stage/command gate.log has never
// seen complete keeps working exactly as it does today, never blocked by a
// floor derived from evidence that does not exist.
func TestRecordedSuiteFloor_NoRecordedRunMeansNoFloorAtAllDirectly(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	got := recordedSuiteFloor("premerge", "cargo nextest run -p never-run")

	if got.Budget != 0 || got.Runs != 0 {
		t.Fatalf("floor = %+v, want the zero floor when gate.log has no completed run to derive one from", got)
	}
}

// TestRecordedSuiteFloor_IgnoresRunsOutsideTheWindow proves the window rule
// directly against recordedSuiteSecs's own filter, rather than only through
// recordedSuiteFloor's aggregate: a run recorded a WINDOW ago (or more) is
// not evidence about what the suite costs NOW, so it must not lower or
// raise today's floor.
func TestRecordedSuiteFloor_IgnoresRunsOutsideTheWindow(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	const stage = "premerge"
	const cmd = "cargo nextest run -p stale-crate"

	AppendGateLog(stage, root, cmd, "green", 300*time.Second)
	got := recordedSuiteSecs(gateLogStageToken(stage), cmd, -time.Second)
	if len(got) != 0 {
		t.Fatalf("recordedSuiteSecs with a negative window = %v, want every recorded run excluded as too old", got)
	}
}
