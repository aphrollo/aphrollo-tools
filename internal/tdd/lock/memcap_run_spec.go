package lock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// RunSlotSpec runs a build slot's child (a suite, a lint, a deferred phase)
// that internal/run starts: it runs spec under the build slot's cap for dir and
// ends it, and everything it started, when its timeout elapses.
func RunSlotSpec(spec run.Spec, dir string) (CapResult, error) {
	return RunSpecCapped(spec, MemCapFor(dir, CapSlot))
}

// RunMutationSpec runs a mutation tool's process tree, started from spec in
// dir, under the pool-sized cap, ending only the runaway worker where it can,
// and ends the whole tree when ctx is done. share is how many such trees run
// side by side (a measurement's shards), which split a derived pool between
// them.
func RunMutationSpec(ctx context.Context, spec run.Spec, dir string, share int) (CapResult, error) {
	return RunSpecCappedCtx(ctx, spec, MemCapFor(dir, CapMutation).splitAmong(share))
}

// RunSpecCapped runs spec as a heavy child of internal/run held to c, and waits
// for it. spec.Env is the child's whole environment (the caller's own when
// nil), never sealed: a shim hands its child the environment it was started
// in. A zero cap runs it uncapped but still guarded. The error is the child's
// own, with run.ErrTimeout joined to it for a child the timeout ended.
//
// A guard that could not be set up never fails the child: it runs unguarded
// and one line on its stderr says so.
func RunSpecCapped(spec run.Spec, c MemCap) (CapResult, error) {
	return RunSpecCappedCtx(context.Background(), spec, c)
}

// RunSpecCappedCtx is RunSpecCapped that ends the child and everything it
// started when ctx is done, and starts nothing when it is done already. The
// child's own exit error is what it answers then, as for a child killed by
// anything else.
func RunSpecCappedCtx(ctx context.Context, spec run.Spec, c MemCap) (CapResult, error) {
	spec.EnvAsIs = true
	spec.MemoryMB = c.MB
	res := CapResult{Cap: c, Mode: "none"}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	var hook *CapRun
	if c.MB > 0 {
		hook = NewCapRun(c)
		spec.Hook = hook
	}
	child, err := run.StartHeavy(ctx, spec)
	if err != nil {
		if hook != nil {
			res = hook.Result(nil)
		}
		return res, err
	}
	stop := child.CloseOnDone(ctx)
	waitErr := child.Wait()
	stop()
	if why := child.Unguarded(); why != nil {
		var w io.Writer = os.Stderr
		if spec.Stderr != nil {
			w = spec.Stderr
		}
		fmt.Fprintf(w, "aphrollo: the child runs unguarded, its process tree is not held in a job (%v)\n", why)
	}
	if hook != nil {
		res = hook.Result(child)
	}
	// A child that exited cleanly but left a grandchild holding its output
	// open is a success: the child's own answer is what counts.
	if errors.Is(waitErr, exec.ErrWaitDelay) && !errors.Is(waitErr, run.ErrTimeout) {
		waitErr = nil
	}
	return res, waitErr
}
