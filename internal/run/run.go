// Package run owns every child process. A light child (git plumbing, a
// formatter) gets a timeout, the plain environment, and a guard that ends its
// whole process tree when the timeout or Close does: a Windows job object that
// kills on close with no breakaway, a process group on unix. A heavy child (a
// build, a test run, lint, local CI, mutation) also gets a governor slot, the
// sealed environment, and the same guard ending its tree whenever the child
// ends, its own exit included, so a test binary stuck in kernel exit or an
// MSYS grandchild that outlives `taskkill /T` is not left holding the box's
// memory.
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
	"github.com/aphrollo/aphrollo-tools/internal/proc"
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
	// Stdin is what the child reads; nil closes it.
	Stdin io.Reader
	// Timeout ends the child and its tree when it elapses. A light child needs
	// one; for a heavy child zero means none.
	Timeout time.Duration
	// PipeGrace is how long Wait lets the output pipes drain once the child has
	// exited or been ended; zero is the default of two seconds.
	PipeGrace time.Duration

	// The rest is for a heavy child.

	// Area is the directory the sealed environment points its git config, temp
	// and state at (gitenv.Sealed).
	Area string
	// EnvAsIs takes Env as the child's whole environment, already built by a
	// caller that has its own rules for it (a runner that needs CI=1, a scratch
	// directory of its own), instead of sealing it with Area. Such a child
	// needs no Area.
	EnvAsIs bool
	// MemoryMB caps the memory of the whole tree, 0 for no cap. Only the
	// Windows job enforces it so far; unix leaves it to the systemd scope or
	// RSS watchdog, which this lane does not build.
	MemoryMB int64
	// Governor, when set, is where the child waits for a slot; Key names its
	// request there (see Governor.Acquire).
	Governor *Governor
	Key      string
	// Hook, when set, brackets the child's life (see Hook).
	Hook Hook
}

// Hook lets a caller bracket a child's life with an enforcer of its own, one
// run does not build itself: a memory cap that rewrites the command into a
// scope and watches the tree while it runs. A hook that has seen Before sees
// Ended exactly once, whether or not the child ever ran.
type Hook interface {
	// Before may rewrite the command before it starts.
	Before(cmd *exec.Cmd)
	// Started is called once the child is running and under its guard.
	Started(pid int)
	// Ended is called once the child has exited, before its guard lets go of
	// what it left behind.
	Ended()
}

// Child is a running child. Its standard input is closed unless the spec gave
// it one.
type Child struct {
	cmd       *exec.Cmd
	tree      tree
	hook      Hook
	exitErr   error
	stop      func() bool // stops the timeout, false when its timer had already fired; true where there is none
	timedOut  atomic.Bool
	unguarded error
	release   func()
	once      sync.Once
	err       error
}

// tree is the guard over a child's process tree, one implementation per OS.
type tree interface {
	// attach brings the started child under the guard.
	attach(p *os.Process) error
	// kill ends the child and everything it started, and may be called again.
	kill()
	// finish ends whatever the child left behind and lets go of the guard.
	finish()
	// peak is the most memory the tree committed, in bytes, where the guard
	// measures it, else 0. It is read once the guard has been finished.
	peak() uint64
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
	if spec.Area == "" && !spec.EnvAsIs {
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
	env := baseEnv(spec.Env)
	if !spec.EnvAsIs {
		env = gitenv.Sealed(env, spec.Area)
	}
	c, err := start(spec, env, true)
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

// prepareGuard builds the guard for a child; a seam for the tests, which
// make it fail the way a box that refuses a job does.
var prepareGuard = prepare

// guardError is a guard that could not be set up or joined: a fact about the
// box that the child never saw, as against a child that failed to start.
type guardError struct{ err error }

func (g guardError) Error() string { return "run: could not guard the child: " + g.err.Error() }
func (g guardError) Unwrap() error { return g.err }

// mayHaveRun is an attach failure after which the child may have run past its
// start: start never runs the command a second time then, and reports it.
type mayHaveRun struct{ err error }

func (m mayHaveRun) Error() string {
	return "run: the child may have run before its guard failed, so it was not started again: " + m.err.Error()
}
func (m mayHaveRun) Unwrap() error { return m.err }

// startHolder is a guard that needs the command to start in a state the caller's
// Before hook may have undone: it puts that state back once the hook has run.
type startHolder interface{ holdStart(cmd *exec.Cmd) }

// plainTree is what a child runs under when its guard could not be set up:
// its own tree, ended by walking from its pid, with the timeout still armed.
// It is no job nor group.
type plainTree struct{ pid int }

func (p *plainTree) attach(child *os.Process) error { p.pid = child.Pid; return nil }
func (p *plainTree) kill() {
	if p.pid > 0 {
		_ = proc.KillTree(p.pid) // the tree may already be gone, which is the goal
	}
}
func (p *plainTree) finish()      {}
func (p *plainTree) peak() uint64 { return 0 }

// start runs the child under its guard. A guard that cannot be set up never
// fails a child: the box could not give one, which says nothing about the code
// the child runs, so the child runs on its own tree walk, with its timeout. A
// heavy child's Unguarded says why; a light child, many and short, says
// nothing.
func start(spec Spec, env []string, heavy bool) (*Child, error) {
	noteStart(spec.Name)
	cmd := command(spec, env)
	t, err := prepareGuard(cmd, heavy, spec.MemoryMB)
	if err == nil {
		c, serr := launch(cmd, t, spec)
		var ge guardError
		if serr == nil || !errors.As(serr, &ge) {
			return c, serr
		}
		err = ge.err
	}
	cmd = command(spec, env)
	cmd.SysProcAttr = proc.TreeAttrs()
	c, serr := launch(cmd, &plainTree{}, spec)
	if serr != nil {
		return nil, serr
	}
	if heavy {
		c.unguarded = err
	}
	return c, nil
}

func command(spec Spec, env []string) *exec.Cmd {
	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Dir, cmd.Env = spec.Dir, env
	cmd.Stdout, cmd.Stderr = spec.Stdout, spec.Stderr
	if spec.Stdin != nil {
		cmd.Stdin = spec.Stdin
	}
	cmd.WaitDelay = pipeGrace
	if spec.PipeGrace > 0 {
		cmd.WaitDelay = spec.PipeGrace
	}
	return cmd
}

// launch starts cmd under t. An attach that fails is a guardError.
func launch(cmd *exec.Cmd, t tree, spec Spec) (*Child, error) {
	if spec.Hook != nil {
		spec.Hook.Before(cmd)
	}
	if h, ok := t.(startHolder); ok {
		h.holdStart(cmd)
	}
	if err := cmd.Start(); err != nil {
		t.finish()
		endHook(spec.Hook)
		return nil, err
	}
	if err := t.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill() // a refusal means it never ran past its start (suspended where a job is used), so start may run it again unguarded; a mayHaveRun refusal is final
		_ = cmd.Wait()
		t.finish()
		endHook(spec.Hook)
		var ran mayHaveRun
		if errors.As(err, &ran) {
			return nil, err
		}
		return nil, guardError{err}
	}
	if spec.Hook != nil {
		spec.Hook.Started(cmd.Process.Pid)
	}
	c := &Child{cmd: cmd, tree: t, hook: spec.Hook, release: func() {}, stop: func() bool { return true }}
	if spec.Timeout > 0 {
		c.stop = afterFunc(spec.Timeout, func() {
			c.timedOut.Store(true)
			c.tree.kill()
		})
	}
	return c, nil
}

// afterFunc starts the timeout timer and returns its stop, which reports false
// when the timer had already fired. Tests replace it.
var afterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// Pid is the child's process id.
func (c *Child) Pid() int { return c.cmd.Process.Pid }

// Wait waits for the child to exit, ends whatever it left behind, and gives
// back its slot. It reports ErrTimeout, joined with the exit error, for a
// child the timeout ended. It may be called again and returns the same answer.
func (c *Child) Wait() error {
	c.once.Do(func() {
		c.exitErr = c.cmd.Wait()
		c.err = c.exitErr
		endHook(c.hook)
		// A timer that had already fired when stopped means the deadline
		// elapsed, even if its callback has not run yet to say so.
		fired := !c.stop()
		c.tree.finish()
		c.release()
		if fired || c.timedOut.Load() {
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

func endHook(h Hook) {
	if h != nil {
		h.Ended()
	}
}

// ExitError waits for the child and reports how it ended, as the OS reported
// it: the error Wait reports without the timeout joined to it. Nil for a
// child that exited cleanly.
func (c *Child) ExitError() error {
	_ = c.Wait()
	return c.exitErr
}

// PeakMemory waits for the child and reports the most memory its tree
// committed, in bytes, where the guard measures it (the Windows job), else 0.
func (c *Child) PeakMemory() uint64 {
	_ = c.Wait()
	return c.tree.peak()
}

// Unguarded is why the child runs without its guard: a heavy child whose job
// object or process group could not be set up. Nil for a child that has one.
func (c *Child) Unguarded() error { return c.unguarded }
