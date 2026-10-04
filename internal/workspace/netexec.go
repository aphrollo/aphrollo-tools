package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
)

// The workspace verbs run unattended in agent sessions: a stalled `git
// fetch`/`git push` (a dropped connection, an SSH host-key prompt, a
// credential-helper prompt) or a hung `gh` call hangs the whole session
// indistinguishably from real work. Every network-touching subprocess in this
// package goes through one of the helpers below, which bound it with a
// deadline and disable terminal credential prompts so a stall fails fast with
// a message naming what stalled, instead of hanging forever.
//
// Two budgets, not one: transferring objects (fetch/push) and a single gh API
// round trip are not the same kind of wait.
var (
	// gitNetworkTimeout bounds `git fetch`/`git push`. A var (not const) so
	// a test can shrink it and prove the deadline actually fires without
	// waiting out a real one.
	gitNetworkTimeout = 120 * time.Second
	// ghTimeout bounds one `gh` call (pr view/create/checks/merge/ready): a
	// single GitHub REST/GraphQL round trip, which normally completes in
	// low single-digit seconds even under load.
	ghTimeout = 60 * time.Second
)

// networkRun runs name+args in dir as a light child of internal/run, ended
// with its whole process tree at timeout, with terminal credential/host-key
// prompts disabled (GIT_TERMINAL_PROMPT=0) so an interactive stall on stdin
// fails on the deadline instead of hanging past it. env is the child's
// environment, this process's when nil. A deadline that ended the child comes
// back as the message networkTimeoutErr writes.
func networkRun(dir string, env []string, timeout time.Duration, stdout, stderr io.Writer, name string, args ...string) error {
	err := lightRun(childrun.Spec{Name: name, Args: args, Dir: dir, Env: env, Timeout: timeout, Stdout: stdout, Stderr: stderr})
	return networkTimeoutErr(errors.Is(err, childrun.ErrTimeout), timeout, name, args, err)
}

// networkTimeoutErr rewrites err into a message naming the stalled command
// and its deadline when the deadline is what actually killed the process,
// leaving any other failure (the subprocess's own real error) untouched.
func networkTimeoutErr(deadlineHit bool, timeout time.Duration, name string, args []string, err error) error {
	if err == nil || !deadlineHit {
		return err
	}
	return fmt.Errorf("%s %s: timed out after %s — check network connectivity/credentials and retry", name, strings.Join(args, " "), timeout)
}

// ghOutput runs a gh subcommand under ghTimeout, in dir, returning just
// stdout (mirroring exec.Cmd.Output).
func ghOutput(dir string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	// stderr-ok: callers classify gh's stdout; this error text reaches no reader
	err := networkRun(dir, nil, ghTimeout, &out, nil, "gh", args...)
	return out.Bytes(), err
}

// ghCombinedOutput runs a gh subcommand under ghTimeout, in dir, returning
// STDOUT ONLY — every caller parses this as DATA (JSON `gh pr view`/`gh api`
// output), and CombinedOutput used to fold a stderr-only line (an update
// notice, a deprecation warning, a proxy or auth note) straight into that
// data while gh still exited 0, breaking the JSON parse (#883). On failure
// the returned bytes come from *exec.ExitError's own captured Stderr instead
// — every caller's error-diagnostic path already reads that byte slice, so
// behaviour there is unchanged, just no longer polluted by stdout noise on
// the success path.
func ghCombinedOutput(dir string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	err := networkRun(dir, nil, ghTimeout, &stdout, &stderr, "gh", args...)
	out := stdout.Bytes()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out = stderr.Bytes()
		} else {
			out = nil
		}
	}
	return out, err
}
