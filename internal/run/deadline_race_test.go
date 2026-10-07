package run

import (
	"errors"
	"testing"
	"time"
)

// A timer can fire, and a fast child exit, before the timer's callback has
// run; Wait then saw timedOut still false and an elapsed deadline went
// unreported. The fake timer here has fired (Stop reports false) without ever
// running its callback, which is that interleaving held still.
func TestWait_ReportsATimeoutWhoseTimerFiredBeforeItsCallbackRan(t *testing.T) {
	prev := afterFunc
	afterFunc = func(time.Duration, func()) func() bool { return func() bool { return false } }
	t.Cleanup(func() { afterFunc = prev })

	spec := helperSpec(t, "exit3")
	spec.Timeout = time.Hour
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWithin(t, c); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, want ErrTimeout: the deadline had elapsed", err)
	}
}

// A child with no timeout has no timer to have fired.
func TestWait_ACallWithNoTimeoutIsNeverReportedAsTimedOut(t *testing.T) {
	c, err := StartHeavy(t.Context(), helperSpec(t, "exit3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWithin(t, c); errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, a child with no timeout cannot time out", err)
	}
}

// On a loaded box the runtime may not service a 1 ns timer while Wait sits in
// the wait call, so the child exits and the timer is stopped before it ever
// fired. The deadline recorded when the timer was armed is then judged by the
// clock: this timer never fires, the clock is past the deadline.
func TestWait_ReportsATimeoutByTheClockWhenTheTimerNeverFiredBeforeTheChildExited(t *testing.T) {
	prevTimer, prevNow := afterFunc, now
	afterFunc = func(time.Duration, func()) func() bool { return func() bool { return true } }
	base := time.Now()
	calls := 0
	now = func() time.Time {
		calls++
		if calls == 1 {
			return base // the moment the timer is armed
		}
		return base.Add(2 * time.Hour)
	}
	t.Cleanup(func() { afterFunc, now = prevTimer, prevNow })

	spec := helperSpec(t, "exit3")
	spec.Timeout = time.Hour
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWithin(t, c); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, want ErrTimeout: the clock was past the deadline when the child exited", err)
	}
}

// A clock still before the deadline is no timeout.
func TestWait_ACallThatExitsBeforeItsDeadlineIsNotTimedOut(t *testing.T) {
	prevTimer, prevNow := afterFunc, now
	afterFunc = func(time.Duration, func()) func() bool { return func() bool { return true } }
	base := time.Now()
	now = func() time.Time { return base }
	t.Cleanup(func() { afterFunc, now = prevTimer, prevNow })

	spec := helperSpec(t, "exit3")
	spec.Timeout = time.Hour
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWithin(t, c); errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, the child exited an hour before its deadline", err)
	}
}
