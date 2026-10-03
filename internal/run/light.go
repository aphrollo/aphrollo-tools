package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// The helpers below are what a caller that ran a child to completion with
// exec.Cmd.Run, Output or CombinedOutput uses instead: the same answers, the
// child guarded and bounded.

// LightCeiling bounds a light child whose caller gave it no limit. run
// refuses a light child with no timeout, and no healthy read of git, go or a
// code generator comes near the ceiling, so it ends only what would otherwise
// have hung its caller.
const LightCeiling = 10 * time.Minute

// stderrKept is how much of a child's stderr LightOutput keeps for the exit
// error, as exec.Cmd.Output keeps a bounded amount.
const stderrKept = 64 << 10

// noPrompt keeps a light git or gh child from asking a terminal a question: on
// unix it leads a process group of its own and a read from /dev/tty would stop
// it until the ceiling, so it fails at once instead.
var noPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1"}

// lightSpec is spec with the environment a light child runs in (the spec's
// own, or this process's, with prompts off) and a timeout.
func lightSpec(spec Spec) Spec {
	env := spec.Env
	if env == nil {
		env = os.Environ()
	}
	spec.Env = append(append([]string(nil), env...), noPrompt...)
	if spec.Timeout <= 0 {
		spec.Timeout = LightCeiling
	}
	return spec
}

// settle is the answer a finished child gives: a child that exited cleanly but
// left a grandchild holding its output open succeeded, as the child's own
// answer is what counts.
func settle(err error) error {
	if errors.Is(err, exec.ErrWaitDelay) && !errors.Is(err, ErrTimeout) {
		return nil
	}
	return err
}

// LightRun runs a light child to completion, its output going where the spec
// says, as exec.Cmd.Run did.
func LightRun(spec Spec) error { return LightRunCtx(context.Background(), spec) }

// LightRunCtx is LightRun that ends the child and its tree when ctx is done.
func LightRunCtx(ctx context.Context, spec Spec) error {
	c, err := StartLight(lightSpec(spec))
	if err != nil {
		return err
	}
	defer c.CloseOnDone(ctx)()
	return settle(c.Wait())
}

// LightOutput runs a light child and returns its stdout, whatever it had
// printed when it failed included, as exec.Cmd.Output did: a spec with no
// Stderr of its own has the child's stderr kept on the *exec.ExitError.
func LightOutput(spec Spec) ([]byte, error) { return LightOutputCtx(context.Background(), spec) }

// LightOutputCtx is LightOutput that ends the child and its tree when ctx is
// done.
func LightOutputCtx(ctx context.Context, spec Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout = &out
	var kept *keepWriter
	if spec.Stderr == nil {
		kept = &keepWriter{limit: stderrKept}
		spec.Stderr = kept
	}
	err := LightRunCtx(ctx, spec)
	var exit *exec.ExitError
	if kept != nil && errors.As(err, &exit) {
		exit.Stderr = kept.buf.Bytes()
	}
	return out.Bytes(), err
}

// LightCombined runs a light child and returns its stdout and stderr together,
// in the order the child wrote them, as exec.Cmd.CombinedOutput did.
func LightCombined(spec Spec) ([]byte, error) {
	return LightCombinedCtx(context.Background(), spec)
}

// LightCombinedCtx is LightCombined that ends the child and its tree when ctx
// is done.
func LightCombinedCtx(ctx context.Context, spec Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout, spec.Stderr = &out, &out
	err := LightRunCtx(ctx, spec)
	return out.Bytes(), err
}

// keepWriter keeps the first limit bytes written to it and drops the rest.
type keepWriter struct {
	buf   bytes.Buffer
	limit int
}

func (k *keepWriter) Write(p []byte) (int, error) {
	if room := k.limit - k.buf.Len(); room > 0 {
		k.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// HeavyRun runs a heavy child to completion in the environment the spec gives
// it (this process's when none), byte for byte, with no timeout unless the
// spec has one and no slot to wait for. A guard that cannot be set up never
// fails the child: it runs unguarded and one line on its stderr says so. A
// child the timeout ended says so, instead of the bare "signal: killed".
func HeavyRun(spec Spec) error {
	spec.EnvAsIs = true
	c, err := StartHeavy(context.Background(), spec)
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
	if errors.Is(waitErr, ErrTimeout) {
		cause := c.ExitError()
		if cause == nil {
			cause = ErrTimeout
		}
		return fmt.Errorf("%s: no answer within %s, killed: %w", spec.Name, spec.Timeout, cause)
	}
	return settle(waitErr)
}

// CloseOnDone ends the child and its whole tree once ctx is done, as
// exec.CommandContext did for the process alone. The returned func stops the
// watch and waits for it to finish; call it once the child's Wait has
// returned, or the watch outlives the child.
func (c *Child) CloseOnDone(ctx context.Context) (stop func()) {
	quit := make(chan struct{})
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		select {
		case <-ctx.Done():
			c.Close()
		case <-quit:
		}
	}()
	return func() {
		close(quit)
		<-gone
	}
}
