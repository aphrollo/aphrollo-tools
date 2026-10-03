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
	"testing"
	"time"
)

// helperSpec is a child that is this test binary playing mode.
func helperSpec(t *testing.T, mode string, args ...string) Spec {
	t.Helper()
	return Spec{Name: os.Args[0], Args: append([]string{noTests}, args...), Env: append(os.Environ(), helperEnv+"="+mode), Area: t.TempDir()}
}

func forceKill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill() // a cleanup of a process that is usually gone already
	}
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
