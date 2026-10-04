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
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
}

// GitHub is the host over GitHub.
type GitHub struct {
	dir     string
	origin  func() string
	timeout time.Duration
	runner  Runner
}

var _ host.Host = (*GitHub)(nil)

// New is a GitHub host for the repository dir sits in.
func New(o Options) *GitHub {
	g := &GitHub{dir: o.Dir, origin: o.Origin, timeout: o.Timeout, runner: o.Runner}
	if g.timeout <= 0 {
		g.timeout = DefaultTimeout
	}
	if g.runner == nil {
		g.runner = ExecRunner
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
	var stdout, stderr bytes.Buffer
	err := run.LightRun(run.Spec{Name: "gh", Args: args, Dir: dir, Timeout: timeout, Stdout: &stdout, Stderr: &stderr})
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
	return g.runner(g.dir, g.timeout, args...)
}
