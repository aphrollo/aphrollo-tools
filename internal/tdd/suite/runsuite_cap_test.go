package suite

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// suiteCapHelperEnv marks the re-executed test binary as the allocating child.
const suiteCapHelperEnv = "SUITETEST_CAP_HELPER_MB"

// TestSuiteCapHelper_Allocate is not a test: it is the runaway RunSuite
// starts under a cap. It touches suiteCapHelperEnv megabytes and then waits
// (bounded) to be ended.
func TestSuiteCapHelper_Allocate(t *testing.T) {
	raw := os.Getenv(suiteCapHelperEnv)
	if raw == "" {
		t.Skip("child process of TestRunSuite_MemoryCap")
	}
	mb, _ := strconv.Atoi(raw)
	b := make([]byte, mb<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	time.Sleep(20 * time.Second)
	_ = b[0]
}

// TestRunSuite_MemoryCapEndsARunawayAsInconclusive is issue #1005 at the
// suite boundary: a run that crosses its cap is ended, and what reaches the
// gates is neither red (Passed) nor a timeout's wording — it is inconclusive,
// reported as OOM-KILLED at the cap, while still carrying TimedOut so every
// consumer that refuses to read a timeout as a failure reads it the same way.
func TestRunSuite_MemoryCapEndsARunawayAsInconclusive(t *testing.T) {
	defer SetMemCapForTest(MemCap{MB: 150, Why: "test"})()
	runner := Runner{
		Cmd:  os.Args[0],
		Args: []string{"-test.run=^TestSuiteCapHelper_Allocate$"},
		Env:  []string{suiteCapHelperEnv + "=600"},
	}
	start := time.Now()
	res := RunSuite(30*time.Second)(runner, t.TempDir())
	if res.Inconclusive != "OOM-KILLED at 0.1 GB" {
		t.Fatalf("Inconclusive = %q (Passed=%v TimedOut=%v Err=%q), want %q", res.Inconclusive, res.Passed, res.TimedOut, res.Err, "OOM-KILLED at 0.1 GB")
	}
	if res.Passed || !res.TimedOut {
		t.Fatalf("Passed=%v TimedOut=%v, want a run that is neither red nor a pass but inconclusive", res.Passed, res.TimedOut)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("took %s to end a 600MB child under a 150MB cap", took)
	}
}

// TestRunSuite_NoHeadroomRefusesWithoutStartingTheRunner: a box with no
// memory to give refuses the start with the reason, inconclusive, and the
// runner is never launched (its command does not exist, so a launch would
// surface as a start error in Err).
func TestRunSuite_NoHeadroomRefusesWithoutStartingTheRunner(t *testing.T) {
	defer SetMemBoxForTest(MemBox{RAMMB: 32768, AvailMB: 500})()
	res := RunSuite(4*time.Second)(Runner{Cmd: "aphrollo-no-such-runner-binary"}, t.TempDir())
	if !strings.HasPrefix(res.Inconclusive, "SKIPPED — memory headroom: 0.5 GB available") {
		t.Fatalf("Inconclusive = %q, want a headroom refusal naming 0.5 GB available", res.Inconclusive)
	}
	if res.Passed || !res.TimedOut {
		t.Fatalf("Passed=%v TimedOut=%v, want inconclusive", res.Passed, res.TimedOut)
	}
	if strings.Contains(res.Err, "executable file not found") {
		t.Fatalf("Err = %q: the runner was started although the box had no headroom", res.Err)
	}
}

// TestRunSuite_HeadroomWaitIsAQuarterOfTheBudgetCapped pins the wait a start
// may spend: a quarter of its budget, never more than two minutes.
func TestRunSuite_HeadroomWaitIsAQuarterOfTheBudgetCapped(t *testing.T) {
	if got := headroomWait(40 * time.Second); got != 10*time.Second {
		t.Errorf("headroomWait(40s) = %s, want 10s", got)
	}
	if got := headroomWait(time.Hour); got != headroomWaitCeiling {
		t.Errorf("headroomWait(1h) = %s, want the ceiling %s", got, headroomWaitCeiling)
	}
}
