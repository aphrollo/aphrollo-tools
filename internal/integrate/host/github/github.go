// Package github is the GitHub adapter of the host port: it implements
// host.Host over the gh CLI, which carries the session's credentials, and
// GitHub's REST and GraphQL APIs behind it.
//
// Nearly every read goes over REST: some sandboxed agent environments refuse
// GraphQL outright (HTTP 403, #880), and the REST routes work wherever it is
// blocked. GraphQL is used only where GitHub publishes nothing else: marking a
// PR ready, a merge queue's positions and its timeline, and enqueueing.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// DefaultTimeout bounds one gh call: a single GitHub round trip, which normally
// completes in low single-digit seconds even under load.
const DefaultTimeout = 60 * time.Second

// Runner runs one gh invocation in dir under a deadline. It answers gh's
// stdout on success; on failure it answers gh's stderr with the error, so a
// caller's message carries gh's own words and never a stdout half-answer.
type Runner func(dir string, timeout time.Duration, args ...string) ([]byte, error)

// Options make a GitHub host.
type Options struct {
	// Dir is the worktree gh runs in; gh resolves {owner}/{repo} from its origin.
	Dir string
	// Origin is the URL of the origin remote, read when owner and repo are
	// needed. A git read, kept out of this package.
	Origin func() string
	// Timeout bounds one gh call; DefaultTimeout when zero.
	Timeout time.Duration
	// Runner is gh itself; the real one when nil. Tests answer for it.
	Runner Runner
	// Env is the environment the real gh runs in. Nil is this process's with
	// every GIT_* variable dropped (gitenv.Clean): a hook's GIT_DIR would
	// otherwise make gh resolve the hook's repository, not Dir's.
	Env []string
	// Context ends the real gh and its tree when it is done, so an interrupt
	// reaches a call in flight; background when nil.
	Context context.Context
	// Deadline bounds the whole run of calls: each is cut to what is left, and
	// fails once nothing is. Zero is no total bound.
	Deadline time.Time
}

// GitHub is the host over GitHub.
type GitHub struct {
	dir      string
	origin   func() string
	timeout  time.Duration
	runner   Runner
	deadline time.Time
}

var _ host.Host = (*GitHub)(nil)

// New is a GitHub host for the repository dir sits in.
func New(o Options) *GitHub {
	g := &GitHub{dir: o.Dir, origin: o.Origin, timeout: o.Timeout, runner: o.Runner, deadline: o.Deadline}
	if g.timeout <= 0 {
		g.timeout = DefaultTimeout
	}
	if g.runner == nil {
		g.runner = execRunner(o.Context, o.Env)
	}
	if g.origin == nil {
		g.origin = func() string { return "" }
	}
	return g
}

// Within is the same host with every call bounded by d.
func (g *GitHub) Within(d time.Duration) host.Host {
	c := *g
	if d > 0 {
		c.timeout = d
	}
	return &c
}

// ExecRunner runs the gh on PATH as a light child, ended with its whole process
// tree when the deadline elapses, with terminal prompts off.
func ExecRunner(dir string, timeout time.Duration, args ...string) ([]byte, error) {
	return execRunner(context.Background(), nil)(dir, timeout, args...)
}

// execRunner is ExecRunner under ctx and env (the scrubbed one when nil).
func execRunner(ctx context.Context, env []string) Runner {
	return func(dir string, timeout time.Duration, args ...string) ([]byte, error) {
		c := ctx
		if c == nil {
			c = context.Background()
		}
		e := env
		if e == nil {
			e = gitenv.Clean()
		}
		return execGH(c, e, dir, timeout, args)
	}
}

func execGH(ctx context.Context, env []string, dir string, timeout time.Duration, args []string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	err := run.LightRunCtx(ctx, run.Spec{Name: "gh", Args: args, Dir: dir, Env: env, Timeout: timeout, Stdout: &stdout, Stderr: &stderr})
	if err == nil {
		return stdout.Bytes(), nil
	}
	if errors.Is(err, run.ErrTimeout) {
		return nil, fmt.Errorf("gh %s: timed out after %s — check network connectivity/credentials and retry", strings.Join(args, " "), timeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stderr.Bytes(), err
	}
	return nil, err
}

// gh runs one gh call under the host's deadline.
func (g *GitHub) gh(args ...string) ([]byte, error) {
	t := g.timeout
	if !g.deadline.IsZero() {
		left := time.Until(g.deadline)
		if left <= 0 {
			return nil, fmt.Errorf("gh %s: the time allowed for this run of calls is spent", strings.Join(args, " "))
		}
		t = min(t, left)
	}
	return g.runner(g.dir, t, args...)
}
