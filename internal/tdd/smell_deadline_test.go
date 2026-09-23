package tdd

import (
	"testing"
)

// Issue #698: `test-sleep` refused nine edits in a week and seven of the
// twenty-one refusals it has ever made were the SAME shape — a select whose
// timer arm bounds another wait:
//
//	select {
//	case args := <-done:
//	        ...
//	case <-time.After(30 * time.Second):
//	        t.Fatal("a mount cycle never terminated")
//	}
//
// That is not a wait. The timer fires only when the thing under test failed to
// answer, so on the happy path it costs zero wall time, and when it does fire
// it turns a HANG into a named failure. Every one of the seven read that way,
// and every one ended with the author rewriting the arm as
// `context.WithTimeout` + `case <-ctx.Done()` — the identical real-time
// deadline the rule had just refused, spelled in a form the regex does not
// know. One (cargo_pathmod_test.go, the refusal this issue was filed from)
// dropped the watchdog altogether and left a comment saying the package
// timeout is now the backstop, so the rule's net effect there was to turn a
// 30-second named failure into a package-wide hang.
//
// The fixture below is that refused edit, verbatim in shape.
const watchdogSelectTest = `package m

import (
	"strings"
	"testing"
	"time"
)

func TestNarrowToRelatedTests_TerminatesOnAMountCycle(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		done <- strings.Join(narrow(base, "a.rs", root), " ")
	}()
	select {
	case args := <-done:
		if !strings.Contains(args, "-E") {
			t.Fatalf("args = %q, want some module filter rather than none", args)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a mount cycle never terminated")
	}
}
`

// TestSmell_TestSleep_ADeadlineArmBoundingAnotherWaitIsNotASleep is the
// narrowing: a timer arm in a select that has another arm is a bound, not a
// wait, so writing one must flow.
func TestSmell_TestSleep_ADeadlineArmBoundingAnotherWaitIsNotASleep(t *testing.T) {
	t.Parallel()
	if blocks(watchdogSelectTest) {
		t.Fatal("a select whose timer arm bounds another wait is a watchdog, not a sleep — refusing it is what made the author delete the watchdog and leave the test free to hang")
	}
}

// TestSmell_TestSleep_ASelectWhoseOnlyArmIsTheTimerIsStillASleep is the half
// that must not regress: `select { case <-time.After(d): }` bounds nothing —
// there is no other arm for it to bound — so it is a sleep wearing a select,
// and it stays refused.
func TestSmell_TestSleep_ASelectWhoseOnlyArmIsTheTimerIsStillASleep(t *testing.T) {
	t.Parallel()
	sleepInDisguise := `package m

import (
	"testing"
	"time"
)

func TestRetry_EventuallySettles(t *testing.T) {
	kick()
	select {
	case <-time.After(200 * time.Millisecond):
	}
	if !settled() {
		t.Fatal("never settled")
	}
}
`
	if !blocks(sleepInDisguise) {
		t.Fatal("a one-armed select on a timer is a sleep with extra syntax; it must still be refused")
	}
}

// TestSmell_TestSleep_ArmsAreCountedAtTheSelectsOwnDepth is the other way the
// must-not-regress half could be lost: count `case` lines anywhere inside the
// select and a switch nested in the timer's own body lends it the second arm it
// does not have, admitting the sleep. Arms belong to the select only at the
// select's own brace depth.
func TestSmell_TestSleep_ArmsAreCountedAtTheSelectsOwnDepth(t *testing.T) {
	t.Parallel()
	sleepWithANestedSwitch := `package m

import (
	"testing"
	"time"
)

func TestRetry_EventuallySettlesUnderEitherMode(t *testing.T) {
	kick(mode)
	select {
	case <-time.After(200 * time.Millisecond):
		switch mode {
		case "fast":
			assertSettled(t)
		default:
			assertQueued(t)
		}
	}
}
`
	if !blocks(sleepWithANestedSwitch) {
		t.Fatal("the select still has one arm; the switch in its body is not a second one, and the timer is still a sleep")
	}
}

// TestSmell_TestSleep_ArmsStopAtTheSelectsOwnClosingBrace is the same
// over-counting failure from the other side: a sleep-select followed by an
// unrelated, properly bounded select must not borrow the second one's arms. The
// count has to end where the select does.
func TestSmell_TestSleep_ArmsStopAtTheSelectsOwnClosingBrace(t *testing.T) {
	t.Parallel()
	sleepThenAWatchdog := `package m

import (
	"testing"
	"time"
)

func TestRetry_SettlesThenReports(t *testing.T) {
	kick()
	select {
	case <-time.After(200 * time.Millisecond):
		note("waited")
	}
	select {
	case got := <-done:
		use(got)
	case <-cancelled:
		t.Fatal("cancelled before the report landed")
	}
}
`
	if !blocks(sleepThenAWatchdog) {
		t.Fatal("the first select has one arm of its own; a later select's arms are not its arms, and the timer is still a sleep")
	}
}
