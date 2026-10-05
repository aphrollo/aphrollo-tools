package suite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// ratchet: test_removed TestSuiteCapHelper_Allocate: it was the 600 MB child of the cap test, which now reads a fake child's kill

// TestRunSuite_MemoryCapEndsARunawayAsInconclusive is issue #1005 at the
// suite boundary: a run the cap ended is neither red (Passed) nor a timeout's
// wording — it is inconclusive, reported as OOM-KILLED at the cap, while still
// carrying TimedOut so every consumer that refuses to read a timeout as a
// failure reads it the same way.
//
// The child is a fake that reports the cap's kill. What ends a real process at
// a cap is the enforcers' own subject (the lock package's monitor tests and
// run's job-object tests); a real 600 MB allocation here only added the box's
// memory headroom, its load and the speed of a kill to a test of how the
// result is read, and failed whenever the box was busy.
func TestRunSuite_MemoryCapEndsARunawayAsInconclusive(t *testing.T) {
	prevWait, prevChild := waitForHeadroomFn, suiteChildFn
	t.Cleanup(func() { waitForHeadroomFn, suiteChildFn = prevWait, prevChild })
	waitForHeadroomFn = func(string, time.Duration) string { return "" }
	defer SetMemCapForTest(MemCap{MB: 150, Why: "test"})()
	var given MemCap
	suiteChildFn = func(_ run.Spec, c MemCap) suiteChildEnd {
		given = c
		return suiteChildEnd{
			err:    errors.New("exit status 1"),
			capped: CapResult{Killed: true, Kills: 1, Cap: c, Mode: "watchdog"},
		}
	}

	res := RunSuite(30*time.Second)(Runner{Cmd: "go", Args: []string{"test", "./..."}}, t.TempDir())

	if given.MB != 150 {
		t.Fatalf("the child was held to %d MB, want the configured 150", given.MB)
	}

	if res.Inconclusive != "OOM-KILLED at 0.1 GB" {
		t.Fatalf("Inconclusive = %q (Passed=%v TimedOut=%v Err=%q), want %q", res.Inconclusive, res.Passed, res.TimedOut, res.Err, "OOM-KILLED at 0.1 GB")
	}
	if res.Passed || !res.TimedOut {
		t.Fatalf("Passed=%v TimedOut=%v, want a run that is neither red nor a pass but inconclusive", res.Passed, res.TimedOut)
	}
}

// A run the cap did not end keeps its own verdict: the same fake child, with
// no kill reported, must not read as inconclusive.
func TestRunSuite_ARunTheCapDidNotEndIsNotInconclusive(t *testing.T) {
	prevWait, prevChild := waitForHeadroomFn, suiteChildFn
	t.Cleanup(func() { waitForHeadroomFn, suiteChildFn = prevWait, prevChild })
	waitForHeadroomFn = func(string, time.Duration) string { return "" }
	suiteChildFn = func(run.Spec, MemCap) suiteChildEnd {
		return suiteChildEnd{capped: CapResult{Cap: MemCap{MB: 150, Why: "test"}, Mode: "watchdog"}}
	}

	res := RunSuite(30*time.Second)(Runner{Cmd: "go", Args: []string{"test", "./..."}}, t.TempDir())

	if res.Inconclusive != "" || !res.Passed {
		t.Fatalf("res = %+v, want a plain pass", res)
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
