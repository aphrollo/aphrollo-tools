//go:build windows

package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// A mutating child (a commit, a merge, an installer) must run once. The job
// join can fail and start falls back to an unguarded start, which is only safe
// while the first process never ran past its suspended start.

// runs is how many lines the child appended to path.
func runs(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "ran\n")
}

// clobberHook is a Before hook that replaces the whole SysProcAttr, dropping
// the creation flags the job guard set (as verbatimCmdLine once did).
type clobberHook struct{}

func (clobberHook) Before(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{} }
func (clobberHook) Started(int)          {}
func (clobberHook) Ended()               {}

// lateAttach is a job guard that lets the child have its way before the join:
// it waits up to a bound for the child to exit, then joins as the real job
// does. A child that ran unsuspended has exited by then and the join fails; a
// suspended one is still there and joins.
type lateAttach struct{ *jobTree }

func (l lateAttach) attach(p *os.Process) error {
	if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(p.Pid)); err == nil {
		_, _ = windows.WaitForSingleObject(h, 500) // a bound, not a condition: a suspended child never exits
		_ = windows.CloseHandle(h)                 // opened here for the wait
	}
	return l.jobTree.attach(p)
}

func TestHeavy_ABeforeHookThatDropsTheSuspendFlagDoesNotRunTheChildTwice(t *testing.T) {
	withGuard(t, func(cmd *exec.Cmd, heavy bool, mb int64) (tree, error) {
		real, err := prepare(cmd, heavy, mb)
		if err != nil {
			return nil, err
		}
		return lateAttach{real.(*jobTree)}, nil
	})
	file := t.TempDir() + `\runs`
	spec := helperSpec(t, "append", file)
	spec.Hook = clobberHook{}
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { forceKill(c.Pid()) })

	if err := waitWithin(t, c); err != nil {
		t.Fatalf("Wait = %v, want the child's own clean exit", err)
	}
	if got := runs(t, file); got != 1 {
		t.Fatalf("the child ran %d times, want exactly once", got)
	}
}

// refusingJoin is a guard whose child starts suspended and whose join fails, as
// a job the box refuses does. It records the pid it was offered.
type refusingJoin struct{ pid int }

func (r *refusingJoin) attach(p *os.Process) error {
	r.pid = p.Pid
	return errors.New("the job refused the child")
}
func (r *refusingJoin) kill()        {}
func (r *refusingJoin) finish()      {}
func (r *refusingJoin) peak() uint64 { return 0 }

func TestHeavy_AFailedJoinOfASuspendedChildLeavesOneRunNotTwo(t *testing.T) {
	refused := &refusingJoin{}
	withGuard(t, func(cmd *exec.Cmd, _ bool, _ int64) (tree, error) {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
		return refused, nil
	})
	file := t.TempDir() + `\runs`
	c, err := StartHeavy(context.Background(), helperSpec(t, "append", file))
	if err != nil {
		t.Fatalf("StartHeavy = %v, want the child run unguarded", err)
	}
	t.Cleanup(func() { forceKill(c.Pid()) })

	if err := waitWithin(t, c); err != nil {
		t.Fatalf("Wait = %v, want the child's own clean exit", err)
	}
	if got := runs(t, file); got != 1 {
		t.Fatalf("the child ran %d times, want exactly once", got)
	}
	if refused.pid == 0 || refused.pid == c.Pid() {
		t.Fatalf("the refused child was pid %d and the one that ran %d, want two distinct pids", refused.pid, c.Pid())
	}
	waitFor(t, "the refused suspended child to be killed", func() bool { return !alive(refused.pid) })
}
