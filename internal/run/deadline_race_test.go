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
