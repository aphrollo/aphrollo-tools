package tdd

import (
	"context"
	"errors"
	"testing"
)

// A measurement that is abandoned must take its child with it. The runner
// holds the box-wide mutation-run lock for the whole spawn, and the caller
// that gives up — a test's own deadline, a cancelled gate run — used to leave
// cargo-mutants compiling behind it with the lock still held: the NEXT
// measurement then waited on mutantsRunLockForever, which is a year, and the
// caller that "timed out" had made the situation it was trying to avoid.
//
// So the tool runs under the caller's context: cancelling it kills the child
// and releases the lock, and the very next measurement acquires it without
// waiting.
func TestMeasure_CancelledContextKillsTheRunAndReleasesTheLock(t *testing.T) {
	abandoned, base := measureFixture(t, laneSource)
	next, nextBase := measureFixture(t, laneSource)
	ctx, cancel := context.WithCancel(context.Background())
	running := make(chan struct{})
	stubMutantsExec(t, func(runCtx context.Context, _ int, _ measuredCall) (int, error) {
		close(running)
		// The child that outlives its caller: it ends when, and only when,
		// the context does.
		<-runCtx.Done()
		return 0, runCtx.Err()
	})

	abandonedRun := make(chan error, 1)
	go func() {
		_, err := MeasureLane(abandoned, MutantsConfig{AtMerge: true}, MeasureOpts{Ctx: ctx, Base: base})
		abandonedRun <- err
	}()
	<-running
	cancel()

	if err := <-abandonedRun; !errors.Is(err, context.Canceled) {
		t.Fatalf("the abandoned measurement answered %v, want the cancellation it was given", err)
	}
	// The lock, now: a second measurement that has to wait for it is the bug
	// this test is about, and waiting is indistinguishable from hanging.
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		writeOutcomes(t, next, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	if release, free := acquireMutantsRunLockWithDeadline("the next measurement", next, 0); !free {
		release()
		t.Fatal("the box-wide mutation-run lock is still held by the run that was cancelled")
	} else {
		release()
	}

	v, err := MeasureLane(next, MutantsConfig{AtMerge: true}, MeasureOpts{Base: nextBase})

	if err != nil {
		t.Fatalf("the next measurement: %v", err)
	}
	if v.Refused || v.Caught != 1 {
		t.Fatalf("verdict = %+v, want the next lane measured normally once the lock came free", v)
	}
}
