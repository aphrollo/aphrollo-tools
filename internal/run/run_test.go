package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// helperSpec is a child that is this test binary playing mode.
func helperSpec(t *testing.T, mode string, args ...string) Spec {
	t.Helper()
	return Spec{Name: os.Args[0], Args: append([]string{noTests}, args...), Env: append(os.Environ(), helperEnv+"="+mode), Area: t.TempDir()}
}

// waitWithin is Wait with a bound: a child whose timeout never fires would
// otherwise hold the test until the helper's own ten-minute watchdog.
func waitWithin(t *testing.T, c *Child) error {
	t.Helper()
	got := make(chan error, 1)
	go func() { got <- c.Wait() }()
	select {
	case err := <-got:
		return err
	case <-time.After(30 * time.Second):
		c.Close()
		t.Fatal("Wait did not return within 30 s of a timeout of under 10 s")
		return nil
	}
}

// closeWithin is Close with a bound: a Close that ends nothing would wait for
// a child that runs until the helper's own ten-minute watchdog.
func closeWithin(t *testing.T, c *Child) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		forceKill(c.Pid())
		t.Fatal("Close did not return within 30 s")
	}
}

func TestBaseEnv_IsThisProcessesEnvironmentOnlyWhenNoneWasGiven(t *testing.T) {
	t.Setenv("RUN_TEST_BASE", "here")

	if got := baseEnv(nil); !slices.Contains(got, "RUN_TEST_BASE=here") {
		t.Errorf("baseEnv(nil) lacks this process's RUN_TEST_BASE: %d entries", len(got))
	}
	given := []string{"ONLY=this"}
	if got := baseEnv(given); !slices.Equal(got, given) {
		t.Errorf("baseEnv(%v) = %v, want the one given", given, got)
	}
	if got := baseEnv([]string{}); len(got) != 0 {
		t.Errorf("baseEnv of an empty environment = %v, want it empty", got)
	}
}

func TestLight_RefusesAChildWithNoTimeout(t *testing.T) {
	spec := helperSpec(t, "exit3")

	if _, err := StartLight(spec); !errors.Is(err, ErrNoTimeout) {
		t.Fatalf("StartLight with no timeout = %v, want ErrNoTimeout", err)
	}
}

func TestLight_ReportsTheChildsExitStatus(t *testing.T) {
	spec := helperSpec(t, "exit3")
	spec.Timeout = 30 * time.Second
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}

	var exit *exec.ExitError
	err = c.Wait()
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("Wait = %v, want exit status 3", err)
	}
	if errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, but the child ended before its timeout", err)
	}
	closeWithin(t, c) // after Wait it must neither hang nor panic
}

func TestLight_TimeoutEndsTheChildAndSaysSo(t *testing.T) {
	spec := helperSpec(t, "sleep")
	spec.Timeout = 300 * time.Millisecond
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Pid()
	t.Cleanup(func() { forceKill(pid) })

	if err := waitWithin(t, c); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, want ErrTimeout", err)
	}
	waitFor(t, "the child to be gone", func() bool { return !alive(pid) })
}

func TestLight_KeepsThePlainEnvironment(t *testing.T) {
	var out bytes.Buffer
	spec := helperSpec(t, "env", "CI", "GITHUB_SHA", "GIT_DIR")
	spec.Env = append(spec.Env, "CI=true", "GITHUB_SHA=abc", "GIT_DIR=/outer")
	spec.Timeout, spec.Stdout = 30*time.Second, &out
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"CI=true", "GITHUB_SHA=abc", "GIT_DIR=/outer"} {
		if !strings.Contains(out.String(), want+"\n") {
			t.Errorf("a light child lost %q: output %q", want, out.String())
		}
	}
}

func TestHeavy_RunsInTheSealedEnvironment(t *testing.T) {
	var out bytes.Buffer
	spec := helperSpec(t, "env", "CI", "GITHUB_SHA", "RUNNER_OS", "GIT_DIR", "KEEP")
	spec.Env = append(spec.Env, "CI=true", "GITHUB_SHA=abc", "RUNNER_OS=Linux", "GIT_DIR=/outer", "KEEP=1")
	spec.Stdout = &out
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}

	if got, want := out.String(), "KEEP=1\n"; got != want {
		t.Fatalf("a heavy child saw %q of the guarded names, want %q", got, want)
	}
}

func TestHeavy_RefusesToStartWithoutASealedArea(t *testing.T) {
	spec := helperSpec(t, "exit3")
	spec.Area = ""

	if _, err := StartHeavy(context.Background(), spec); !errors.Is(err, ErrNoArea) {
		t.Fatalf("StartHeavy with no area = %v, want ErrNoArea", err)
	}
}

func TestHeavy_HoldsItsSlotUntilClosed(t *testing.T) {
	g := NewGovernor(1)
	spec := helperSpec(t, "sleep")
	spec.Governor = g
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Pid()
	t.Cleanup(func() { forceKill(pid) })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second child got a slot while the first ran: err = %v", err)
	}

	closeWithin(t, c)
	acquire(t, g, "")() // the slot came back with the Close
}

func TestHeavy_ReleasesItsSlotWhenItsStartFails(t *testing.T) {
	g := NewGovernor(1)
	spec := Spec{Name: filepath.Join(t.TempDir(), "no-such-binary"), Area: t.TempDir(), Governor: g}

	if _, err := StartHeavy(context.Background(), spec); err == nil {
		t.Fatal("StartHeavy of a missing binary succeeded")
	}

	acquire(t, g, "")() // a slot kept by the failed start would time this out
}

func TestHeavy_WaitsForASlotInsideTheCallersContext(t *testing.T) {
	g := NewGovernor(1)
	held := acquire(t, g, "")
	defer held()
	spec := helperSpec(t, "exit3")
	spec.Governor = g
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := StartHeavy(ctx, spec); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartHeavy with no slot free = %v, want context.DeadlineExceeded", err)
	}
}

func TestLight_ReadsTheStdinItWasGiven(t *testing.T) {
	var out bytes.Buffer
	spec := helperSpec(t, "stdin")
	spec.Stdin, spec.Stdout, spec.Timeout = strings.NewReader("fed in\n"), &out, 30*time.Second
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}

	if got, want := out.String(), "fed in\n"; got != want {
		t.Fatalf("the child read %q from its stdin, want %q", got, want)
	}
}

func TestHeavy_EnvAsIsKeepsTheCallersEnvironmentAndNeedsNoArea(t *testing.T) {
	var out bytes.Buffer
	spec := helperSpec(t, "env", "CI", "GIT_DIR", "KEEP")
	spec.Env = append(spec.Env, "CI=1", "GIT_DIR=/outer", "KEEP=1")
	spec.Area, spec.EnvAsIs, spec.Stdout = "", true, &out
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatalf("StartHeavy with EnvAsIs and no area: %v", err)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}

	if got, want := out.String(), "CI=1\nGIT_DIR=/outer\nKEEP=1\n"; got != want {
		t.Fatalf("an EnvAsIs child saw %q of the guarded names, want %q: run sealed an environment the caller had already built", got, want)
	}
}

// hookLog records what a Hook was told, in order.
type hookLog struct {
	mu     sync.Mutex
	events []string
	pid    int
}

func (h *hookLog) note(e string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, e)
}

func (h *hookLog) Before(cmd *exec.Cmd) {
	if cmd == nil || cmd.Path == "" {
		h.note("before without a command")
		return
	}
	h.note("before")
}
func (h *hookLog) Started(pid int) { h.pid = pid; h.note("started") }
func (h *hookLog) Ended()          { h.note("ended") }

func (h *hookLog) got() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.events, ",")
}

func TestHeavy_HookSeesTheChildBeforeItStartsAndAfterItEnds(t *testing.T) {
	h := &hookLog{}
	spec := helperSpec(t, "exit3")
	spec.Hook = h
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.got(); got != "before,started" || h.pid != c.Pid() {
		t.Fatalf("after the start the hook saw %q (pid %d), want before,started with pid %d", got, h.pid, c.Pid())
	}
	_ = c.Wait()
	_ = c.Wait() // a second Wait ends nothing twice

	if got, want := h.got(), "before,started,ended"; got != want {
		t.Fatalf("the hook saw %q, want %q", got, want)
	}
}

func TestHeavy_HookIsToldWhenTheStartFails(t *testing.T) {
	h := &hookLog{}
	spec := Spec{Name: filepath.Join(t.TempDir(), "no-such-binary"), Area: t.TempDir(), Hook: h}

	if _, err := StartHeavy(context.Background(), spec); err == nil {
		t.Fatal("StartHeavy of a missing binary succeeded")
	}

	if got, want := h.got(), "before,ended"; got != want {
		t.Fatalf("the hook saw %q, want %q: what it set up for a child that never ran is never torn down", got, want)
	}
}

func TestHeavy_ExitErrorIsTheChildsOwnEndWithoutTheTimeoutJoined(t *testing.T) {
	spec := helperSpec(t, "sleep")
	spec.Timeout = 300 * time.Millisecond
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { forceKill(c.Pid()) })
	if err := waitWithin(t, c); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, want ErrTimeout", err)
	}

	exit := c.ExitError()
	var ee *exec.ExitError
	if !errors.As(exit, &ee) || errors.Is(exit, ErrTimeout) {
		t.Fatalf("ExitError = %v, want the child's own *exec.ExitError with no timeout in it", exit)
	}
}

// brokenTree is a guard that cannot be set up: its attach (or the prepare
// that built it) fails, as on a box where the process already runs inside a
// job that refuses a nested one.
type brokenTree struct{ attachErr error }

func (b *brokenTree) attach(*os.Process) error { return b.attachErr }
func (b *brokenTree) kill()                    {}
func (b *brokenTree) finish()                  {}
func (b *brokenTree) peak() uint64             { return 0 }

func withGuard(t *testing.T, fn func(cmd *exec.Cmd, heavy bool, memoryMB int64) (tree, error)) {
	t.Helper()
	prev := prepareGuard
	prepareGuard = fn
	t.Cleanup(func() { prepareGuard = prev })
}

// A guard that cannot be set up is a fact about the box, never about the code
// under test: the child runs unguarded, still with its timeout, and says so.
func TestHeavy_AGuardThatFailsToAttachRunsTheChildUnguarded(t *testing.T) {
	boom := errors.New("the job refused the child")
	withGuard(t, func(*exec.Cmd, bool, int64) (tree, error) { return &brokenTree{attachErr: boom}, nil })
	var out bytes.Buffer
	spec := helperSpec(t, "env", "KEEP")
	spec.Env = append(spec.Env, "KEEP=1")
	spec.Stdout = &out
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatalf("StartHeavy with a guard that cannot attach = %v, want the child run unguarded", err)
	}

	if err := c.Wait(); err != nil {
		t.Fatalf("Wait = %v, want the child's own clean exit", err)
	}
	if got, want := out.String(), "KEEP=1\n"; got != want {
		t.Fatalf("the unguarded child printed %q, want %q", got, want)
	}
	if !errors.Is(c.Unguarded(), boom) {
		t.Fatalf("Unguarded = %v, want the reason the guard failed", c.Unguarded())
	}
}

func TestHeavy_AGuardThatFailsToPrepareRunsTheChildUnguarded(t *testing.T) {
	boom := errors.New("no job object for this process")
	withGuard(t, func(*exec.Cmd, bool, int64) (tree, error) { return nil, boom })
	spec := helperSpec(t, "exit3")
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatalf("StartHeavy with a guard that cannot be built = %v, want the child run unguarded", err)
	}

	var exit *exec.ExitError
	if err := c.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("Wait = %v, want the child's own exit status 3: it ran", err)
	}
	if !errors.Is(c.Unguarded(), boom) {
		t.Fatalf("Unguarded = %v, want the reason the guard failed", c.Unguarded())
	}
}

func TestHeavy_AnUnguardedChildStillEndsAtItsTimeout(t *testing.T) {
	withGuard(t, func(*exec.Cmd, bool, int64) (tree, error) { return &brokenTree{attachErr: errors.New("refused")}, nil })
	spec := helperSpec(t, "sleep")
	spec.Timeout = 300 * time.Millisecond
	c, err := StartHeavy(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Pid()
	t.Cleanup(func() { forceKill(pid) })

	if err := waitWithin(t, c); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Wait = %v, want ErrTimeout", err)
	}
	waitFor(t, "the unguarded child to be gone", func() bool { return !alive(pid) })
}

func TestHeavy_AGuardedChildReportsNoUnguardedReason(t *testing.T) {
	c, err := StartHeavy(context.Background(), helperSpec(t, "exit3"))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Wait()

	if c.Unguarded() != nil {
		t.Fatalf("Unguarded = %v for a child that got its guard", c.Unguarded())
	}
}

func TestHeavy_AMissingBinaryIsStillAStartFailureNotAGuardFailure(t *testing.T) {
	spec := Spec{Name: filepath.Join(t.TempDir(), "no-such-binary"), Area: t.TempDir()}

	if _, err := StartHeavy(context.Background(), spec); err == nil {
		t.Fatal("StartHeavy of a missing binary succeeded")
	}
}
