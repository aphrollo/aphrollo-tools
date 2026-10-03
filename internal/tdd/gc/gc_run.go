package gc

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// Every child a sweep starts, bar the detached sweep itself (see
// startDetachedGC), goes through internal/run, which ends the child's whole
// process tree when its timeout elapses.

// gcLightCeiling bounds a read-only probe (tasklist, pgrep, git plumbing,
// cargo metadata) that had no bound of its own. run refuses a light child
// with no timeout, and no healthy call of these comes near the ceiling, so it
// ends only what would otherwise have hung the sweep.
const gcLightCeiling = 10 * time.Minute

// gcNoPrompt keeps a light git child from asking a terminal a question: on
// unix it leads a process group of its own and a read from /dev/tty would stop
// it until the ceiling, so it fails at once instead.
var gcNoPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1"}

// gcLightRun runs a light child to completion, its output going where the
// spec says, as exec.Cmd.Run did. A child that exited cleanly but left a
// grandchild holding its output open succeeded: the child's own answer is
// what counts.
func gcLightRun(spec run.Spec) error {
	env := spec.Env
	if env == nil {
		env = os.Environ()
	}
	spec.Env = append(append([]string(nil), env...), gcNoPrompt...)
	if spec.Timeout <= 0 {
		spec.Timeout = gcLightCeiling
	}
	c, err := run.StartLight(spec)
	if err != nil {
		return err
	}
	err = c.Wait()
	if errors.Is(err, exec.ErrWaitDelay) && !errors.Is(err, run.ErrTimeout) {
		return nil
	}
	return err
}

// gcLightOutput runs a light child and returns its stdout, whatever it had
// printed when it failed included, as exec.Cmd.Output did.
func gcLightOutput(spec run.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout = &out
	err := gcLightRun(spec)
	return out.Bytes(), err
}

// gcLightCombined runs a light child and returns its stdout and stderr
// together, in the order it wrote them, as exec.Cmd.CombinedOutput did.
func gcLightCombined(spec run.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout, spec.Stderr = &out, &out
	err := gcLightRun(spec)
	return out.Bytes(), err
}
