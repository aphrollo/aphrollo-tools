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

// RunSlotSpec is RunSlotChild for a child that internal/run starts: it runs
// spec under the build slot's cap for dir and ends it, and everything it
// started, when its timeout elapses.
func RunSlotSpec(spec run.Spec, dir string) (CapResult, error) {
	return RunSpecCapped(spec, MemCapFor(dir, CapSlot))
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
	spec.EnvAsIs = true
	spec.MemoryMB = c.MB
	res := CapResult{Cap: c, Mode: "none"}
	var hook *CapRun
	if c.MB > 0 {
		hook = NewCapRun(c)
		spec.Hook = hook
	}
	child, err := run.StartHeavy(context.Background(), spec)
	if err != nil {
		if hook != nil {
			res = hook.Result(nil)
		}
		return res, err
	}
	waitErr := child.Wait()
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
