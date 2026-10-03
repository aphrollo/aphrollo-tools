// Package run owns every child process. A light child (git plumbing, a
// formatter) gets a timeout and the plain environment. A heavy child (a
// build, a test run, lint, local CI, mutation) also gets a governor slot, the
// sealed environment, and a guard that ends its whole process tree: a Windows
// job object that kills on close with no breakaway, a process group on unix.
// Whatever ends a child (Close, the timeout, or its own exit) ends what it
// started, so a test binary stuck in kernel exit or an MSYS grandchild that
// outlives `taskkill /T` is not left holding the box's memory.
package run

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/gitenv"
)

var (
	// ErrTimeout is what Wait reports for a child the timeout ended.
	ErrTimeout = errors.New("run: the child timed out and its process tree was ended")
	// ErrNoTimeout refuses a light child that has no timeout.
	ErrNoTimeout = errors.New("run: a light child needs a timeout")
	// ErrNoArea refuses a heavy child that has no sealed area to run in.
	ErrNoArea = errors.New("run: a heavy child needs a sealed area")
)

// pipeGrace is how long Wait lets a child's output pipes drain after it has
// exited: a grandchild that holds one open must not hold Wait with it.
const pipeGrace = 2 * time.Second

// Spec is one child.
type Spec struct {
	Name string
	Args []string
	Dir  string
	// Env is the child's environment, or this process's when nil. A heavy
	// child gets it sealed.
	Env            []string
	Stdout, Stderr io.Writer
	// Timeout ends the child and its tree when it elapses. A light child needs
	// one; for a heavy child zero means none.
	Timeout time.Duration

	// The rest is for a heavy child.

	// Area is the directory the sealed environment points its git config, temp
	// and state at (gitenv.Sealed).
	Area string
	// MemoryMB caps the memory of the whole tree, 0 for no cap. Only the
	// Windows job enforces it so far; unix leaves it to the systemd scope or
	// RSS watchdog, which this lane does not build.
	MemoryMB int64
	// Governor, when set, is where the child waits for a slot; Key names its
	// request there (see Governor.Acquire).
	Governor *Governor
	Key      string
}

// Child is a running child. Its standard input is closed.
type Child struct {
	cmd      *exec.Cmd
	tree     tree
	stop     func() bool // stops the timeout; a no-op where there is none
	timedOut atomic.Bool
	release  func()
	once     sync.Once
	err      error
}

// tree is the guard over a child's process tree, one implementation per OS.
type tree interface {
	// attach brings the started child under the guard.
	attach(p *os.Process) error
	// kill ends the child and everything it started, and may be called again.
	kill()
	// finish ends whatever the child left behind and lets go of the guard.
	finish()
}

// StartLight starts a light child: a timeout, the plain environment, no slot.
func StartLight(spec Spec) (*Child, error) {
	if spec.Timeout <= 0 {
		return nil, ErrNoTimeout
	}
	return start(spec, spec.Env, false)
}

// StartHeavy starts a heavy child once the governor, if the spec names one,
// gives it a slot; ctx bounds that wait. The slot is held until Wait or Close
// has ended the child.
func StartHeavy(ctx context.Context, spec Spec) (*Child, error) {
	if spec.Area == "" {
		return nil, ErrNoArea
	}
	release := func() {}
	if spec.Governor != nil {
		r, err := spec.Governor.Acquire(ctx, spec.Key)
		if err != nil {
			return nil, err
		}
		release = r
	}
	c, err := start(spec, gitenv.Sealed(baseEnv(spec.Env), spec.Area), true)
	if err != nil {
		release()
		return nil, err
	}
	c.release = release
	return c, nil
}

// baseEnv is the environment a child starts from: the one given, or this
// process's when none was.
func baseEnv(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}

func start(spec Spec, env []string, heavy bool) (*Child, error) {
	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Dir, cmd.Env = spec.Dir, env
	cmd.Stdout, cmd.Stderr = spec.Stdout, spec.Stderr
	cmd.WaitDelay = pipeGrace
	t, err := prepare(cmd, heavy, spec.MemoryMB)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		t.finish()
		return nil, err
	}
	if err := t.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill() // the child never joined its guard and must not run unguarded
		_ = cmd.Wait()
		t.finish()
		return nil, err
	}
	c := &Child{cmd: cmd, tree: t, release: func() {}, stop: func() bool { return false }}
	if spec.Timeout > 0 {
		c.stop = time.AfterFunc(spec.Timeout, func() {
			c.timedOut.Store(true)
			c.tree.kill()
		}).Stop
	}
	return c, nil
}

// Pid is the child's process id.
func (c *Child) Pid() int { return c.cmd.Process.Pid }

// Wait waits for the child to exit, ends whatever it left behind, and gives
// back its slot. It reports ErrTimeout, joined with the exit error, for a
// child the timeout ended. It may be called again and returns the same answer.
func (c *Child) Wait() error {
	c.once.Do(func() {
		c.err = c.cmd.Wait()
		c.stop()
		c.tree.finish()
		c.release()
		if c.timedOut.Load() {
			c.err = errors.Join(ErrTimeout, c.err)
		}
	})
	return c.err
}

// Close ends the child and its whole tree and waits for it to be gone. It is
// safe after Wait and safe to call twice.
func (c *Child) Close() {
	c.tree.kill()
	_ = c.Wait() // the exit error of a child that was just killed is the kill
}
