package suite

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

func TestLightOutput_ReturnsWhatTheChildPrintedAndFeedsItsStdin(t *testing.T) {
	out, err := lightOutput(run.Spec{
		Name: gitBinary(), Args: []string{"hash-object", "--stdin"}, Dir: t.TempDir(),
		Env: cleanGitEnv(), Stdin: strings.NewReader("hello\n"),
	})

	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(out)), "ce013625030ba8dba906f756967f9e9ca394464a"; got != want {
		t.Fatalf("git hash-object --stdin printed %q for \"hello\n\", want %q", got, want)
	}
}

func TestLightOutput_AFailingChildKeepsItsExitStatusAndItsStdout(t *testing.T) {
	out, err := lightOutput(run.Spec{
		Name: gitBinary(), Args: []string{"cat-file", "-p", "0000000000000000000000000000000000000000"}, Dir: t.TempDir(),
		Env: cleanGitEnv(),
	})

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("err = %v, want the child's *exec.ExitError with a nonzero status, as cmd.Output reported it", err)
	}
	if len(out) != 0 {
		t.Fatalf("stdout = %q, want what the child printed before it failed, which is nothing", out)
	}
}

func TestLightOutput_ATimeoutEndsTheChildAndSaysSo(t *testing.T) {
	bash := runtest.BashCommand()
	if bash == "" {
		t.Skip("no bash on this box") // skip-ok: the child that outlives its timeout needs a shell, which a box may lack.
	}
	start := time.Now()

	_, err := lightOutput(run.Spec{Name: bash, Args: []string{"-c", "sleep 60"}, Dir: t.TempDir(), Timeout: time.Second})

	if !errors.Is(err, run.ErrTimeout) {
		t.Fatalf("err = %v, want the run package's timeout", err)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("took %s to end a child with a 1 s timeout", took)
	}
}

func TestRunSuiteChild_ABudgetAlreadySpentNeverStartsTheChild(t *testing.T) {
	end := runSuiteChild(run.Spec{Name: "aphrollo-no-such-runner-binary", Timeout: -time.Second}, MemCap{})

	if !end.timedOut || !errors.Is(end.err, context.DeadlineExceeded) {
		t.Fatalf("end = %+v, want a timeout with context.DeadlineExceeded and no start error", end)
	}
}

func TestRunSuiteChild_AMissingCommandIsAStartFailureNotATimeout(t *testing.T) {
	end := runSuiteChild(run.Spec{Name: "aphrollo-no-such-runner-binary", Dir: t.TempDir(), Timeout: time.Minute}, MemCap{})

	if end.timedOut || end.err == nil || !strings.Contains(end.err.Error(), "executable file not found") {
		t.Fatalf("end = %+v, want the start failure and no timeout", end)
	}
}

// The wait for memory sits inside the run's budget, as it did when the budget
// was a context made before the wait: a box short of memory that waits W and
// then runs for the whole budget would outlast the Runner.Deadline that
// runCargoLocked carved its own lock wait out of, and the hook's outer kill
// would fire first, leaving no verdict.
func TestRunSuite_TheHeadroomWaitIsSpentInsideTheBudget(t *testing.T) {
	const budget = 10 * time.Minute
	const waited = 2 * time.Second
	fakeSuiteClock(t, waited)
	var given time.Duration
	suiteChildFn = func(spec run.Spec, _ MemCap) suiteChildEnd { given = spec.Timeout; return suiteChildEnd{} }

	RunSuite(budget)(Runner{Cmd: "aphrollo-no-such-runner-binary"}, t.TempDir())

	if given != budget-waited {
		t.Fatalf("the child was given %s, want the budget %s less the %s waited for memory", given, budget, waited)
	}
}

// A budget the wait for memory used up never starts the child.
func TestRunSuite_ABudgetSpentWaitingForMemoryNeverStartsTheChild(t *testing.T) {
	fakeSuiteClock(t, 300*time.Millisecond)
	var spec run.Spec
	suiteChildFn = func(s run.Spec, memCap MemCap) suiteChildEnd { spec = s; return runSuiteChild(s, memCap) }

	res := RunSuite(100*time.Millisecond)(Runner{Cmd: "aphrollo-no-such-runner-binary"}, t.TempDir())

	if !res.TimedOut || res.Passed || res.Inconclusive != "" {
		t.Fatalf("res = %+v, want a timeout with no start failure: the budget was spent before the child could start", res)
	}
	if spec.Timeout > 0 {
		t.Fatalf("the child was given %s of a budget already spent", spec.Timeout)
	}
}

// A light git or gh child has no terminal to prompt on; on unix it sits in a
// process group of its own and a prompt on /dev/tty would stop it until the
// ceiling. It has to fail at once instead.
func TestLightOutput_NeverPromptsOnATerminal(t *testing.T) {
	out, err := lightOutput(run.Spec{
		Name: gitBinary(), Args: []string{"-c", "alias.showenv=!env", "showenv"}, Dir: t.TempDir(), Env: cleanGitEnv(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1"} {
		if !strings.Contains(string(out), want+"\n") {
			t.Errorf("a light child's environment lacks %s", want)
		}
	}
}

// fakeSuiteClock makes the wait for memory take exactly waited of a clock the
// test owns, and restores the real seams after the test.
func fakeSuiteClock(t *testing.T, waited time.Duration) {
	t.Helper()
	prevWait, prevChild, prevNow := waitForHeadroomFn, suiteChildFn, suiteNowFn
	t.Cleanup(func() { waitForHeadroomFn, suiteChildFn, suiteNowFn = prevWait, prevChild, prevNow })
	now := time.Unix(1_000_000, 0)
	suiteNowFn = func() time.Time { return now }
	waitForHeadroomFn = func(string, time.Duration) string { now = now.Add(waited); return "" }
}
