package suite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// Every child this package starts goes through internal/run, which ends the
// child's whole process tree when its timeout elapses or it is closed: a
// Windows job object that kills on close, a process group elsewhere. A test
// binary stuck in kernel exit or an MSYS grandchild that `taskkill /T` misses
// is not left holding the box's memory after a suite is given up on.

// lightCeiling bounds a read-only child (git plumbing, `go list`, `cargo
// metadata`, `gh`) that had no bound of its own. run refuses a light child
// with no timeout, and no healthy call of these comes near the ceiling, so it
// ends only what would otherwise have hung the gate.
const lightCeiling = 10 * time.Minute

// Seams of RunSuite, which the tests drive: the box's wait for memory, the
// clock the budget is cut by, and the start of the child itself.
var (
	waitForHeadroomFn = WaitForHeadroom
	suiteNowFn        = time.Now
	suiteChildFn      = runSuiteChild
)

// noPrompt keeps a light git or gh child from asking a terminal a question: on
// unix it leads a process group of its own and a read from /dev/tty would stop
// it until the ceiling, so it fails at once instead.
var noPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1"}

// lightOutput runs a light child to completion and returns its stdout, as
// exec.Cmd.Output did, whatever stdout it had printed when it failed
// included. A zero Timeout is lightCeiling. A child that exited cleanly but
// left a grandchild holding its output open is a success, as it is for a
// suite: the child's own answer is what counts.
func lightOutput(spec run.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout = &out
	spec.Env = append(append([]string(nil), baseEnvOf(spec.Env)...), noPrompt...)
	if spec.Timeout <= 0 {
		spec.Timeout = lightCeiling
	}
	c, err := run.StartLight(spec)
	if err != nil {
		return nil, err
	}
	err = c.Wait()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	return out.Bytes(), err
}

// suiteChildEnd is how a heavy child ended.
type suiteChildEnd struct {
	// err is the child's own end: an exit status, a refusal to start, or
	// context.DeadlineExceeded for a budget already spent.
	err error
	// timedOut is true when the timeout ended the child and its tree.
	timedOut bool
	// capped is what the memory cap did to it; zero for an uncapped run.
	capped CapResult
}

// runSuiteChild starts a heavy child under run, held to memCap, and waits for
// it. spec.Env is the child's whole environment, already built. A spec whose
// Timeout is spent never starts, as a context past its deadline never did.
func runSuiteChild(spec run.Spec, memCap MemCap) suiteChildEnd {
	if spec.Timeout <= 0 {
		return suiteChildEnd{err: context.DeadlineExceeded, timedOut: true}
	}
	spec.EnvAsIs = true
	spec.MemoryMB = memCap.MB
	var hook *CapRun
	if memCap.MB > 0 {
		hook = NewCapRun(memCap)
		spec.Hook = hook
	}
	child, err := run.StartHeavy(context.Background(), spec)
	if err != nil {
		end := suiteChildEnd{err: err}
		if hook != nil {
			end.capped = hook.Result(nil)
		}
		return end
	}
	waitErr := child.Wait()
	if why := child.Unguarded(); why != nil {
		fmt.Fprintf(os.Stderr, "gate: the suite child runs unguarded, its process tree is not held in a job (%v)\n", why)
	}
	end := suiteChildEnd{err: child.ExitError(), timedOut: errors.Is(waitErr, run.ErrTimeout)}
	if hook != nil {
		end.capped = hook.Result(child)
	}
	return end
}

// baseEnvOf is the environment a child starts from: the one given, or this
// process's when none was.
func baseEnvOf(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}
