package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

func bashOrSkip(t *testing.T) string {
	t.Helper()
	bash := runtest.BashCommand()
	if bash == "" {
		t.Skip("no bash on this box") // skip-ok: the child is a shell, which a box may lack.
	}
	return bash
}

func TestLightOutput_ReturnsStdoutAndTheExitErrorOfAFailingChild(t *testing.T) {
	bash := bashOrSkip(t)
	var errb bytes.Buffer
	out, err := lightOutput(run.Spec{Name: bash, Args: []string{"-c", "echo partial; echo why >&2; exit 4"}, Stderr: &errb})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("err = %v, want the exit status 4", err)
	}
	if string(out) != "partial\n" {
		t.Fatalf("stdout = %q, want what the child printed before it failed", out)
	}
	if errb.String() != "why\n" {
		t.Fatalf("stderr = %q, want it carried to the caller's writer", errb.String())
	}
}

func TestLightOutput_ChildNeverPromptsAndKeepsTheCallersEnvironment(t *testing.T) {
	bash := bashOrSkip(t)
	t.Setenv("F9_CALLER_MARK", "kept")
	out, err := lightOutput(run.Spec{Name: bash, Args: []string{"-c", `printf '%s|%s|%s' "$GIT_TERMINAL_PROMPT" "$GH_PROMPT_DISABLED" "$F9_CALLER_MARK"`}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "0|1|kept" {
		t.Fatalf("child saw %q, want a prompt-free child in the caller's own environment", out)
	}
}

func TestLightOutput_AnEnvironmentOfTheSpecsOwnIsKeptWholeAndStillPromptFree(t *testing.T) {
	bash := bashOrSkip(t)
	t.Setenv("F9_CALLER_MARK", "from-the-box")
	env := append(os.Environ(), "F9_SPEC_MARK=given")
	out, err := lightOutput(run.Spec{Name: bash, Env: env, Args: []string{"-c", `printf '%s|%s' "$F9_SPEC_MARK" "$GIT_TERMINAL_PROMPT"`}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "given|0" {
		t.Fatalf("child saw %q, want the spec's environment with prompts off", out)
	}
}

func TestLightCombined_InterleavesStdoutAndStderrInTheOrderTheChildWroteThem(t *testing.T) {
	bash := bashOrSkip(t)
	out, err := lightCombined(run.Spec{Name: bash, Args: []string{"-c", "echo one; echo two >&2; echo three"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "one\ntwo\nthree\n" {
		t.Fatalf("combined = %q, want the three lines in order", out)
	}
}

func TestLightRun_MissingProgramIsAStartErrorNotAnExit(t *testing.T) {
	err := lightRun(run.Spec{Name: filepath.Join(t.TempDir(), "no-such-program")})
	if err == nil {
		t.Fatal("a program that does not exist reported success")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		t.Fatalf("a child that never started was reported as an exit: %v", err)
	}
}

func TestLightRun_TimeoutEndsTheChildAndSaysSo(t *testing.T) {
	bash := bashOrSkip(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	err := lightRun(run.Spec{Name: bash, Args: []string{"-c", `echo $$ > "$0"; exec sleep 600`, filepath.ToSlash(pidFile)}, Timeout: 3 * time.Second})
	if !errors.Is(err, run.ErrTimeout) {
		t.Fatalf("err = %v, want the timeout that ended the child", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("a child given 3s held the caller for %s", elapsed)
	}
	pids := runtest.ReadPids(pidFile)
	t.Cleanup(func() {
		for _, pid := range pids {
			runtest.ForceKill(pid)
		}
	})
	if len(pids) == 0 {
		t.Fatal("the child never recorded its pid, so nothing is proven")
	}
}

func TestLightOutputCtx_ACancelEndsTheChildAndReportsTheContext(t *testing.T) {
	bash := bashOrSkip(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	defer cancel()
	start := time.Now()
	_, err := lightOutputCtx(ctx, run.Spec{Name: bash, Args: []string{"-c", "exec sleep 600"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("a cancelled child held the caller for %s", elapsed)
	}
}

func TestBoundedRun_TimeoutSaysItTimedOutAndEndsAnMSYSShellChain(t *testing.T) {
	bash := bashOrSkip(t)
	for _, heavy := range []bool{true, false} {
		name := "light"
		if heavy {
			name = "heavy"
		}
		t.Run(name, func(t *testing.T) {
			if !heavy && runtime.GOOS == "windows" {
				t.Skip("a light child's tree is walked by pid, which misses an MSYS shell's children; only a heavy child is held in a job") // skip-ok: the chain proof belongs to the heavy guard.
			}
			dir := t.TempDir()
			script := filepath.Join(dir, "chain.sh")
			if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(dir, "pids")
			spec := run.Spec{Name: bash, Args: []string{filepath.ToSlash(script), filepath.ToSlash(pidFile), "wait"}}

			err := boundedRun(8*time.Second, spec, heavy)

			pids := runtest.ReadPids(pidFile)
			t.Cleanup(func() {
				for _, pid := range pids {
					runtest.ForceKill(pid)
				}
			})
			if err == nil || !strings.Contains(err.Error(), "no answer within 8s, killed") {
				t.Fatalf("err = %v, want it to say the child gave no answer within its budget and was killed", err)
			}
			if len(pids) < 3 {
				t.Fatalf("the chain recorded %d pids before the budget, want 3: it never came up", len(pids))
			}
			if !runtest.AllGone(pids, 20*time.Second) {
				t.Errorf("a pid of the shell chain %v outlived the child that was killed at its budget", pids)
			}
		})
	}
}

func TestBoundedRun_AFailingChildIsNotCalledATimeout(t *testing.T) {
	bash := bashOrSkip(t)
	err := boundedRun(time.Minute, run.Spec{Name: bash, Args: []string{"-c", "exit 3"}}, true)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("err = %v, want the child's own exit status 3 and no timeout wording", err)
	}
}
