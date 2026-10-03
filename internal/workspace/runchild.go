package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
)

// Every child this package starts goes through internal/run, which ends the
// child's whole process tree when its timeout elapses or it is closed: a
// Windows job object that kills on close for a heavy child, a process group
// on unix. A read of git or gh is a light child; a dependency install or a
// verify step (a build, a test run) is a heavy one, in the environment it was
// given and with no slot to wait for.

// lightCeiling bounds a read-only or local-write git child that had no bound
// of its own. run refuses a light child with no timeout, and no healthy call
// of these comes near the ceiling, so it ends only what would otherwise have
// hung the verb.
const lightCeiling = 10 * time.Minute

// commitCeiling bounds `git commit`, whose pre-commit hook is the whole gate
// (vet, lint, the fail-first proof). The commit had no bound before; an hour
// is long past any healthy gate and ends only one that hung.
const commitCeiling = time.Hour

// noPrompt keeps a light git or gh child from asking a terminal a question: on
// unix it leads a process group of its own and a read from /dev/tty would stop
// it until the ceiling, so it fails at once instead.
var noPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1"}

// lightSpec is spec with the environment a light child runs in (the spec's
// own, or this process's, with prompts off) and a timeout.
func lightSpec(spec childrun.Spec) childrun.Spec {
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

// settle is the answer a finished child gives: a child that exited cleanly but
// left a grandchild holding its output open succeeded, as the child's own
// answer is what counts.
func settle(err error) error {
	if errors.Is(err, exec.ErrWaitDelay) && !errors.Is(err, childrun.ErrTimeout) {
		return nil
	}
	return err
}

// lightRun runs a light child to completion, its output going where the spec
// says, as exec.Cmd.Run did.
func lightRun(spec childrun.Spec) error {
	c, err := childrun.StartLight(lightSpec(spec))
	if err != nil {
		return err
	}
	return settle(c.Wait())
}

// lightOutput runs a light child and returns its stdout, whatever it had
// printed when it failed included, as exec.Cmd.Output did. Its stderr goes to
// the spec's writer, or nowhere.
func lightOutput(spec childrun.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout = &out
	err := lightRun(spec)
	return out.Bytes(), err
}

// lightCombined runs a light child and returns its stdout and stderr together,
// in the order the child wrote them, as exec.Cmd.CombinedOutput did.
func lightCombined(spec childrun.Spec) ([]byte, error) {
	var out bytes.Buffer
	spec.Stdout, spec.Stderr = &out, &out
	err := lightRun(spec)
	return out.Bytes(), err
}

// lightGit runs git with args and returns its stdout.
func lightGit(args ...string) ([]byte, error) {
	return lightOutput(childrun.Spec{Name: "git", Args: args})
}

// lightGitCombined runs git with args and returns its stdout and stderr
// together.
func lightGitCombined(args ...string) ([]byte, error) {
	return lightCombined(childrun.Spec{Name: "git", Args: args})
}

// lightGitOK reports whether git with args exited cleanly.
func lightGitOK(args ...string) bool {
	return lightRun(childrun.Spec{Name: "git", Args: args}) == nil
}

// heavyLimit ends a heavy child (a dependency install, a verify step) that has
// no limit of its own once it elapses. Zero is none, as these steps always
// ran: a real install or test run has no healthy upper bound. A var so a test
// can shrink it and prove the whole tree ends, without waiting out a real one.
var heavyLimit time.Duration

// heavyRun runs a heavy child to completion in the environment the spec gives
// it (this process's when none), byte for byte, with no timeout unless the
// spec or heavyLimit has one. A guard that cannot be set up never fails the
// child: it runs unguarded and one line on its stderr says so. A child the
// timeout ended says so, instead of the bare "signal: killed".
func heavyRun(spec childrun.Spec) error {
	spec.EnvAsIs = true
	if spec.Timeout <= 0 {
		spec.Timeout = heavyLimit
	}
	c, err := childrun.StartHeavy(context.Background(), spec)
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
	if errors.Is(waitErr, childrun.ErrTimeout) {
		cause := c.ExitError()
		if cause == nil {
			cause = childrun.ErrTimeout
		}
		return fmt.Errorf("%s: no answer within %s, killed: %w", spec.Name, spec.Timeout, cause)
	}
	return settle(waitErr)
}
