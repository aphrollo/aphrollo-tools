package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
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

// networkCmd builds name+args under ctx, running in dir, with terminal
// credential/host-key prompts disabled (GIT_TERMINAL_PROMPT=0) so an
// interactive stall on stdin fails on the deadline instead of hanging past
// it.
func networkCmd(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
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

// gitNetworkOutput runs a git subcommand that touches the network (fetch,
// push) under gitNetworkTimeout, in dir, returning combined stdout+stderr
// like exec.Cmd.CombinedOutput.
func gitNetworkOutput(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	cmd := networkCmd(ctx, dir, "git", args...)
	out, err := cmd.CombinedOutput()
	return out, networkTimeoutErr(ctx.Err() == context.DeadlineExceeded, gitNetworkTimeout, "git", args, err)
}

// gitNetworkStream runs a git subcommand that touches the network with its
// output streamed live to stdout/stderr (push's progress meter) rather than
// buffered — same deadline and prompt suppression as gitNetworkOutput.
func gitNetworkStream(dir string, stdout, stderr io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
	defer cancel()
	cmd := networkCmd(ctx, dir, "git", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	return networkTimeoutErr(ctx.Err() == context.DeadlineExceeded, gitNetworkTimeout, "git", args, err)
}

// ghOutput runs a gh subcommand under ghTimeout, in dir, returning just
// stdout (mirroring exec.Cmd.Output).
func ghOutput(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := networkCmd(ctx, dir, "gh", args...)
	// stderr-ok: the sole caller (ghCIStatus) classifies gh's stdout and reads
	// any failure as "none" — this error text reaches no reader
	out, err := cmd.Output()
	return out, networkTimeoutErr(ctx.Err() == context.DeadlineExceeded, ghTimeout, "gh", args, err)
}

// ghCombinedOutput runs a gh subcommand under ghTimeout, in dir, returning
// STDOUT ONLY — every caller parses this as DATA (JSON `gh pr view`/`gh api`
// output), and CombinedOutput used to fold a stderr-only line (an update
// notice, a deprecation warning, a proxy or auth note) straight into that
// data while gh still exited 0, breaking the JSON parse (#883). On failure
// the returned bytes come from *exec.ExitError's own captured Stderr instead
// — every caller's error/absence handling (isNoPRError et al.) already reads
// that byte slice, so behaviour there is unchanged, just no longer polluted
// by stdout noise on the success path.
func ghCombinedOutput(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := networkCmd(ctx, dir, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out = ee.Stderr
		} else {
			out = nil
		}
	}
	return out, networkTimeoutErr(ctx.Err() == context.DeadlineExceeded, ghTimeout, "gh", args, err)
}
