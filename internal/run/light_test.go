package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLightOutput_ReturnsWhatAFailingChildWroteAndItsStderrOnTheExitError(t *testing.T) {
	out, err := LightOutput(helperSpec(t, "fail"))

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("err = %v, want exit status 4", err)
	}
	if string(out) != "partial\n" {
		t.Errorf("stdout = %q, want what the child printed before it failed", out)
	}
	if string(exit.Stderr) != "why\n" {
		t.Errorf("ExitError.Stderr = %q, want the child's own stderr, as exec.Cmd.Output carries it", exit.Stderr)
	}
}

func TestLightOutput_SendsStderrWhereTheSpecSaysAndLeavesTheExitErrorsStderrEmpty(t *testing.T) {
	var errb bytes.Buffer
	spec := helperSpec(t, "fail")
	spec.Stderr = &errb

	_, err := LightOutput(spec)

	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want an exit error", err)
	}
	if errb.String() != "why\n" || len(exit.Stderr) != 0 {
		t.Errorf("writer got %q and ExitError.Stderr %q, want the writer to have it all", errb.String(), exit.Stderr)
	}
}

func TestLightCombined_KeepsBothStreamsInTheOrderTheChildWroteThem(t *testing.T) {
	out, err := LightCombined(helperSpec(t, "fail"))

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("err = %v, want exit status 4", err)
	}
	if string(out) != "partial\nwhy\n" {
		t.Errorf("output = %q, want both streams together", out)
	}
}

func TestLightRun_NeedsNoTimeoutOfItsOwnAndRunsPromptFreeInTheGivenEnvironment(t *testing.T) {
	var out bytes.Buffer
	spec := helperSpec(t, "env", "RUN_TEST_MARK", "GIT_TERMINAL_PROMPT", "GH_PROMPT_DISABLED")
	spec.Env = append(spec.Env, "RUN_TEST_MARK=given")
	spec.Stdout = &out

	if err := LightRun(spec); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"RUN_TEST_MARK=given", "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("child saw %q, want it to include %s", out.String(), want)
		}
	}
}

func TestLightRun_TimeoutEndsTheChildAndSaysSo(t *testing.T) {
	spec := helperSpec(t, "sleep")
	spec.Timeout = 300 * time.Millisecond

	err := LightRun(spec)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

// A light child's guard walks the tree from its pid, which does not reach an
// MSYS grandchild on Windows; only the heavy child's job does. So the chain
// here is the test binary's own, and the shell chain is proved for the heavy.
func TestLightRunCtx_CancelEndsTheChildAndItsGrandchild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	spec := chains()[0].spec(t, pidFile, false)
	spec.Timeout = 5 * time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- LightRunCtx(ctx, spec) }()

	waitFor(t, "the tree to come up", func() bool { return len(readPids(pidFile)) >= 2 })
	pids := readPids(pidFile)
	watch(t, pids)
	cancel()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("LightRunCtx did not return within 30 s of its context ending")
	}
	assertTreeGone(t, pids)
}

func TestHeavyRun_TimeoutEndsTheWholeTreeAndSaysTheChildGaveNoAnswer(t *testing.T) {
	for _, ch := range chains() {
		t.Run(ch.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pids")
			spec := ch.spec(t, pidFile, false)
			spec.Timeout = 8 * time.Second

			err := HeavyRun(spec)

			pids := readPids(pidFile)
			watch(t, pids)
			if err == nil || !strings.Contains(err.Error(), "no answer within 8s") {
				t.Fatalf("err = %v, want it to say the child gave no answer within 8s and was killed", err)
			}
			if len(pids) < ch.pids {
				t.Fatalf("the tree recorded %d pids, want %d: it never came up", len(pids), ch.pids)
			}
			assertTreeGone(t, pids)
		})
	}
}

func TestHeavyRun_GivesTheChildTheEnvironmentItWasHandedAndAddsNothing(t *testing.T) {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT=") {
			env = append(env, kv)
		}
	}
	var out bytes.Buffer
	spec := helperSpec(t, "env", "RUN_TEST_MARK", "GIT_TERMINAL_PROMPT")
	spec.Area = ""
	spec.Env = append(env, helperEnv+"=env", "RUN_TEST_MARK=given")
	spec.Stdout = &out

	if err := HeavyRun(spec); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "RUN_TEST_MARK=given\n" {
		t.Errorf("child saw %q, want only the mark: the environment as handed over, no prompt variable added", got)
	}
}

func TestHeavyRun_AFailingChildKeepsItsOwnExitStatusAndIsNotCalledATimeout(t *testing.T) {
	err := HeavyRun(helperSpec(t, "exit3"))

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("err = %v, want the child's own exit status 3 and no timeout wording", err)
	}
}

func TestCloseOnDone_StopLeavesTheChildRunningWhenTheContextEndsLater(t *testing.T) {
	spec := helperSpec(t, "sleep")
	spec.Timeout = 5 * time.Minute
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Pid()
	t.Cleanup(func() { forceKill(pid) })
	ended := make(chan struct{})
	go func() {
		_ = c.Wait()
		close(ended)
	}()
	ctx, cancel := context.WithCancel(context.Background())

	stop := c.CloseOnDone(ctx)
	stop()
	cancel()

	select {
	case <-ended:
		t.Fatal("the child ended after its watch was stopped")
	case <-time.After(300 * time.Millisecond):
	}
	closeWithin(t, c)
}

func TestCloseOnDone_EndsTheChildWhenTheContextEnds(t *testing.T) {
	spec := helperSpec(t, "sleep")
	spec.Timeout = 5 * time.Minute
	c, err := StartLight(spec)
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Pid()
	t.Cleanup(func() { forceKill(pid) })
	ctx, cancel := context.WithCancel(context.Background())
	stop := c.CloseOnDone(ctx)
	defer stop()

	cancel()

	waitFor(t, "the child to be gone", func() bool { return !alive(pid) })
}

func TestCommand_PipeGraceIsTheSpecsWhenSetAndTwoSecondsOtherwise(t *testing.T) {
	if got := command(Spec{Name: "x"}, nil).WaitDelay; got != 2*time.Second {
		t.Errorf("default WaitDelay = %s, want 2s", got)
	}
	if got := command(Spec{Name: "x", PipeGrace: 7 * time.Second}, nil).WaitDelay; got != 7*time.Second {
		t.Errorf("WaitDelay = %s, want the spec's 7s", got)
	}
}

func TestKeepWriter_KeepsTheFirstBytesUpToItsLimitAndStillAcceptsEverything(t *testing.T) {
	k := &keepWriter{limit: 10}

	for _, chunk := range []string{"abcdef", "ghijkl", "mnop"} {
		if n, err := k.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v, want all %d bytes accepted", chunk, n, err, len(chunk))
		}
	}

	if got := k.buf.String(); got != "abcdefghij" {
		t.Errorf("kept %q, want the first 10 bytes %q", got, "abcdefghij")
	}
}

func TestHeavyRun_AGuardThatCannotBeSetUpRunsTheChildAndSaysSoOnItsStderr(t *testing.T) {
	boom := errors.New("no job object for this process")
	withGuard(t, func(*exec.Cmd, bool, int64) (tree, error) { return nil, boom })
	var errb bytes.Buffer
	spec := helperSpec(t, "exit3")
	spec.Stderr = &errb

	err := HeavyRun(spec)

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("err = %v, want the child's own exit status 3: it ran", err)
	}
	if want := "the child runs unguarded"; !strings.Contains(errb.String(), want) || !strings.Contains(errb.String(), boom.Error()) {
		t.Errorf("stderr = %q, want a line saying %q and why", errb.String(), want)
	}
}

func TestHeavyRun_AGuardedChildSaysNothingAboutAGuard(t *testing.T) {
	var errb bytes.Buffer
	spec := helperSpec(t, "exit3")
	spec.Stderr = &errb

	_ = HeavyRun(spec)

	if errb.Len() != 0 {
		t.Errorf("stderr = %q, want nothing from a child that ran under its guard", errb.String())
	}
}
