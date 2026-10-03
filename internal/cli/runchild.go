package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// Every child this package starts, bar the few that hand the user's own
// terminal to a program (see execGit and the `cargo run` branch of
// execCargoEnv), goes through internal/run, which ends the child's whole
// process tree when its timeout elapses or it is closed: a Windows job object
// that kills on close for a heavy child, a process group elsewhere.

// lightCeiling bounds a read-only child (git plumbing, gh, reg) that had no
// bound of its own. run refuses a light child with no timeout, and no healthy
// call of these comes near the ceiling, so it ends only what would otherwise
// have hung the verb.
const lightCeiling = 10 * time.Minute

// noPrompt keeps a light git or gh child from asking a terminal a question: on
// unix it leads a process group of its own and a read from /dev/tty would stop
// it until the ceiling, so it fails at once instead.
var noPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1"}

// lightSpec is spec with the environment a light child runs in (the spec's
// own, or this process's, with prompts off) and a timeout.
func lightSpec(spec run.Spec) run.Spec {
	env := spec.Env
	if env == nil {
		env = os.Environ()
	}
	spec.Env = append(append([]string(nil), env...), noPrompt...)
	if spec.Timeout <= 0 {
		spec.Timeout = lightCeiling
	}
	return spec
}

// settle is the answer a finished light child gives: a child that exited
// cleanly but left a grandchild holding its output open succeeded, as the
// child's own answer is what counts.
func settle(err error) error {
	if errors.Is(err, exec.ErrWaitDelay) && !errors.Is(err, run.ErrTimeout) {
		return nil
	}
	return err
}

// lightRun runs a light child to completion, its output going where the spec
// says, as exec.Cmd.Run did.
func lightRun(spec run.Spec) error {
	c, err := run.StartLight(lightSpec(spec))
	if err != nil {
		return err
	}
	return settle(c.Wait())
}

// lightOutput runs a light child and returns its stdout, whatever it had
// printed when it failed included, as exec.Cmd.Output did. Its stderr goes to
// the spec's writer, or nowhere.
func lightOutput(spec run.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout = &out
	err := lightRun(spec)
	return out.Bytes(), err
}

// lightCombined runs a light child and returns its stdout and stderr together,
// in the order the child wrote them, as exec.Cmd.CombinedOutput did.
func lightCombined(spec run.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout, spec.Stderr = &out, &out
	err := lightRun(spec)
	return out.Bytes(), err
}

// lightOutputCtx is lightOutput that ends the child and its tree when ctx is
// done, and then answers with the context's error, as exec.CommandContext did.
func lightOutputCtx(ctx context.Context, spec run.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout = &out
	c, err := run.StartLight(lightSpec(spec))
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-done:
		}
	}()
	err = settle(c.Wait())
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.Bytes(), err
}

// boundedRun runs spec to completion, killing it and its tree once budget has
// passed. A heavy child (a build) runs in the environment it was given, as
// the caller's own; a light one is a read or a short command. A kill by the
// budget says so, instead of the bare "signal: killed".
func boundedRun(budget time.Duration, spec run.Spec, heavy bool) error {
	spec.Timeout = budget
	var c *run.Child
	var err error
	if heavy {
		spec.EnvAsIs = true
		c, err = run.StartHeavy(context.Background(), spec)
	} else {
		c, err = run.StartLight(lightSpec(spec))
	}
	if err != nil {
		return err
	}
	waitErr := c.Wait()
	if why := c.Unguarded(); why != nil {
		var w io.Writer = os.Stderr
		if spec.Stderr != nil {
			w = spec.Stderr
		}
		fmt.Fprintf(w, "aphrollo: the child runs unguarded, its process tree is not held in a job (%v)\n", why)
	}
	if errors.Is(waitErr, run.ErrTimeout) {
		cause := c.ExitError()
		if cause == nil {
			cause = run.ErrTimeout
		}
		return fmt.Errorf("no answer within %s, killed: %w", budget, cause)
	}
	return settle(waitErr)
}
