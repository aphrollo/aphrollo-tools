package postedit

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// A deferred run is abandoned at a ceiling and, for `go test`, handed a -timeout
// above it. Both were one flat 600s, so a package whose suite takes 607s alone
// ended without a verdict every time, and spent the CPU of a full run to say
// nothing. The ceiling is now sized from the command's own recorded durations.

// budgetSeams injects the recorded history of a command and the box's load, and
// counts the reads of the load, which a floor-only answer must not make.
func budgetSeams(t *testing.T, history []float64, loadPct float64, loadOK bool) (loadReads *int) {
	t.Helper()
	prevHist, prevLoad := budgetHistoryFn, budgetLoadFn
	reads := 0
	budgetHistoryFn = func(string, []string) []float64 { return history }
	budgetLoadFn = func() (float64, bool) { reads++; return loadPct, loadOK }
	t.Cleanup(func() { budgetHistoryFn, budgetLoadFn = prevHist, prevLoad })
	return &reads
}

func budgetRepeat(secs float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = secs
	}
	return out
}

// A suite that took 600s at its p90 must be given more than 600s, or the run
// that finishes at 607s is killed one clock short of its verdict.
func TestSizeDeferredBudget_AP90Of600SecondsGetsMoreThan600(t *testing.T) {
	budgetSeams(t, budgetRepeat(600, 10), 0, true)

	got := sizeDeferredBudget("/repo", []string{"go", "test", "./internal/workspace"})

	if want := 900 * time.Second; got.Budget != want {
		t.Fatalf("budget = %s, want %s (p90 600s × 1.5 headroom × 1.00 load)", got.Budget, want)
	}
}

// No record, no change: a command the log has never seen keeps today's value,
// and the box's load is not even read for it.
func TestSizeDeferredBudget_NoHistoryKeepsTheFloorAndReadsNoLoad(t *testing.T) {
	reads := budgetSeams(t, nil, 90, true)

	got := sizeDeferredBudget("/repo", []string{"go", "test", "./internal/cli"})

	if got.Budget != deferredMax() {
		t.Fatalf("budget = %s, want the floor %s", got.Budget, deferredMax())
	}
	if *reads != 0 {
		t.Fatalf("the load was read %d times for a command with no history; the floor needs no load", *reads)
	}
}

// A fast suite is never given less than today's ceiling: the floor holds.
func TestSizeDeferredBudget_AFastSuiteKeepsTheFloor(t *testing.T) {
	budgetSeams(t, budgetRepeat(10, 10), 0, true)

	got := sizeDeferredBudget("/repo", []string{"go", "test", "./internal/tdd"})

	if got.Budget != deferredMax() {
		t.Fatalf("budget = %s, want the floor %s for a 10s suite", got.Budget, deferredMax())
	}
}

// The cap holds: a record of a suite that hangs cannot ask for an unbounded
// ceiling, or a wedged process is never reported.
func TestSizeDeferredBudget_TheCapHolds(t *testing.T) {
	budgetSeams(t, budgetRepeat(5000, 10), 100, true)

	got := sizeDeferredBudget("/repo", []string{"go", "test", "./..."})

	if want := 3600 * time.Second; got.Budget != want {
		t.Fatalf("budget = %s, want the cap %s", got.Budget, want)
	}
}

// A busy box stretches the budget: load 100% doubles it (600 × 1.5 × 2).
func TestSizeDeferredBudget_TheBoxLoadScalesIt(t *testing.T) {
	budgetSeams(t, budgetRepeat(600, 10), 100, true)

	got := sizeDeferredBudget("/repo", []string{"go", "test", "./internal/workspace"})

	if want := 1800 * time.Second; got.Budget != want {
		t.Fatalf("budget = %s, want %s at 100%% load", got.Budget, want)
	}
}

// A load that cannot be read counts as no load, not as a refusal to size.
func TestSizeDeferredBudget_AnUnreadableLoadIsNoLoad(t *testing.T) {
	budgetSeams(t, budgetRepeat(600, 10), 0, false)

	got := sizeDeferredBudget("/repo", []string{"go", "test", "./internal/workspace"})

	if want := 900 * time.Second; got.Budget != want {
		t.Fatalf("budget = %s, want %s", got.Budget, want)
	}
}

// The tail decides, not the middle: eight fast runs and two slow ones is a p90
// of the slow one, the run that times a suite out.
func TestSizeDeferredBudget_ReadsTheP90NotTheMedian(t *testing.T) {
	hist := append(budgetRepeat(20, 8), 800, 800)
	budgetSeams(t, hist, 0, true)

	got := sizeDeferredBudget("/repo", []string{"go", "test", "./internal/cli"})

	if want := 1200 * time.Second; got.Budget != want {
		t.Fatalf("budget = %s, want %s (p90 800s × 1.5)", got.Budget, want)
	}
}

// The why is one line a reader can check: the budget, the measurement it came
// from and the factors.
func TestDeferredBudgetNote_QuotesTheMeasurementAndTheFactors(t *testing.T) {
	budgetSeams(t, budgetRepeat(600, 10), 0, true)
	got := sizeDeferredBudget("/repo", []string{"go", "test", "./internal/workspace"}).Note()
	want := "budget 900s, from measured p90 600s × 1.5 headroom × 1.00 load (10 runs)"
	if got != want {
		t.Errorf("sized note = %q, want %q", got, want)
	}

	budgetSeams(t, nil, 0, true)
	got = sizeDeferredBudget("/repo", []string{"go", "test", "./internal/cli"}).Note()
	want = "budget 600s, the floor: no recorded run of this command"
	if got != want {
		t.Errorf("floor note = %q, want %q", got, want)
	}

	budgetSeams(t, budgetRepeat(5000, 10), 0, true)
	got = sizeDeferredBudget("/repo", []string{"go", "test", "./..."}).Note()
	want = "budget 3600s, the cap: measured p90 5000s × 1.5 headroom × 1.00 load asks for 7500s"
	if got != want {
		t.Errorf("capped note = %q, want %q", got, want)
	}
}

func TestDeferredBudgetNote_IsOneLine(t *testing.T) {
	budgetSeams(t, budgetRepeat(600, 3), 37.5, true)
	note := sizeDeferredBudget("/r", []string{"go", "test", "./x"}).Note()
	if strings.ContainsAny(note, "\r\n") {
		t.Fatalf("note spans lines: %q", note)
	}
}

// A run that ends without a verdict says why in its own line.
func TestDeferredAbandonedLine_SaysWhatTheBudgetWas(t *testing.T) {
	j := DeferredJob{Phase: "run", BudgetNote: "budget 900s, from measured p90 600s × 1.5 headroom × 1.00 load (10 runs)"}

	got := deferredAbandonedLineFor("/repo", j, 901*time.Second)

	want := "gate: → deferred-abandoned (the deferred run phase in /repo was killed after 901s with no result — that edit's code was NOT tested; budget 900s, from measured p90 600s × 1.5 headroom × 1.00 load (10 runs))"
	if got != want {
		t.Fatalf("line = %q\nwant   %q", got, want)
	}
}

// A record from before the budget existed keeps its old line.
func TestDeferredAbandonedLine_AnOldRecordKeepsTheOldLine(t *testing.T) {
	got := deferredAbandonedLineFor("/repo", DeferredJob{Phase: "run"}, 601*time.Second)

	want := "gate: → deferred-abandoned (the deferred run phase in /repo was killed after 601s with no result — that edit's code was NOT tested)"
	if got != want {
		t.Fatalf("line = %q\nwant   %q", got, want)
	}
}

// The ceiling a job is abandoned at is the one it was started with: a 700s run
// of a job sized to 900s is still going, an unsized one is past its 600s.
func TestDeferredExpired_UsesTheBudgetTheJobWasSizedTo(t *testing.T) {
	now := time.Now()
	started := now.Add(-700 * time.Second)

	if deferredExpired(DeferredJob{Started: started, BudgetSecs: 900}, now) {
		t.Error("a 700s run of a job sized to 900s was abandoned")
	}
	if !deferredExpired(DeferredJob{Started: started}, now) {
		t.Error("a 700s run of an unsized job (600s floor) was not abandoned")
	}
	if !deferredExpired(DeferredJob{Started: now.Add(-901 * time.Second), BudgetSecs: 900}, now) {
		t.Error("a 901s run of a job sized to 900s was not abandoned")
	}
}

// The go test -timeout sits above the job's own ceiling, so the ceiling stays
// the only clock that ends the run.
func TestPhaseArgvFor_TheGoTimeoutFollowsTheBudget(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./internal/workspace"}}

	argv := phaseArgvFor(r, "run", 900*time.Second)

	if !slices.Contains(argv, "-timeout=20m0s") {
		t.Fatalf("argv = %v, want -timeout=20m0s (the 900s budget plus the 5m slack)", argv)
	}
}

// A twin of a run differs only by the timeout its budget gave it; that is not
// a different run, so it still coalesces.
func TestSameRunArgv_IgnoresTheTimeout(t *testing.T) {
	a := []string{"go", "test", "-timeout=20m0s", "-count=1", "./x"}
	b := []string{"go", "test", "-timeout=15m0s", "-count=1", "./x"}
	if !sameRunArgv(a, b) {
		t.Error("two argv differing only by -timeout were taken for different runs")
	}
	if sameRunArgv(a, []string{"go", "test", "-timeout=20m0s", "-count=1", "./y"}) {
		t.Error("two different packages were taken for one run")
	}
}

// Recorded history: the green runs and the abandoned ones of the same command,
// whatever -timeout and -count the deferred phase added to its spelling.
func TestRecordedRunSecs_ReadsGreenAndAbandonedRunsOfTheSameCommand(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	AppendGateLog("postedit", root, "go test ./internal/workspace", "green", 300*time.Second)
	AppendGateLog("postedit", root, "go test -timeout=15m0s -count=1 ./internal/workspace", DeferredAbandoned, 601*time.Second)
	AppendGateLog("postedit", root, "go test ./internal/workspace", "red", 2*time.Second)
	AppendGateLog("postedit", root, "go test ./internal/workspace", "deferred", 0)
	AppendGateLog("postedit", root, "go test ./internal/cli", "green", 99*time.Second)

	got := recordedRunSecs(root, []string{"go", "test", "-timeout=20m0s", "-count=1", "./internal/workspace"})

	if want := []float64{300, 601}; !slices.Equal(got, want) {
		t.Fatalf("recorded secs = %v, want %v: the green and the abandoned run, not the red, the deferred marker or the other package", got, want)
	}
}
