package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
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

// shrinkHeavyLimit gives the heavy children of the package a limit short
// enough to wait out, and puts the old one back.
func shrinkHeavyLimit(t *testing.T, d time.Duration) {
	t.Helper()
	prev := heavyLimit
	heavyLimit = d
	t.Cleanup(func() { heavyLimit = prev })
}

// chainScript writes the MSYS shell chain (a shell, a shell, a sleep) and
// returns the script and the file its pids are recorded in.
func chainScript(t *testing.T) (script, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "chain.sh")
	if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(script), filepath.ToSlash(filepath.Join(dir, "pids"))
}

// requireChainEnded fails unless the chain the child started came up and is
// now entirely gone: the proof the child's whole tree ended with it, not just
// its own process.
func requireChainEnded(t *testing.T, pidFile string, err error) {
	t.Helper()
	pids := runtest.ReadPids(pidFile)
	t.Cleanup(func() {
		for _, pid := range pids {
			runtest.ForceKill(pid)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("err = %v, want it to say the child gave no answer within its limit and was killed", err)
	}
	if len(pids) < 3 {
		t.Fatalf("the chain recorded %d pids before the limit, want 3: it never came up", len(pids))
	}
	if !runtest.AllGone(pids, 20*time.Second) {
		t.Errorf("a pid of the shell chain %v outlived the child that was killed at its limit", pids)
	}
}

func TestHeavyRun_TimeoutEndsAnMSYSShellChain(t *testing.T) {
	bash := bashOrSkip(t)
	script, pidFile := chainScript(t)
	err := heavyRun(childrun.Spec{Name: bash, Args: []string{script, pidFile, "wait"}, Timeout: 8 * time.Second})
	requireChainEnded(t, pidFile, err)
}

func TestVerifyRun_LimitEndsTheStepsWholeTree(t *testing.T) {
	bash := bashOrSkip(t)
	shrinkHeavyLimit(t, 8*time.Second)
	script, pidFile := chainScript(t)
	err := verifyRun([]string{bash, script, pidFile, "wait"}, t.TempDir(), io.Discard, io.Discard)
	requireChainEnded(t, pidFile, err)
}

func TestRunStep_LimitEndsAnInstallStepsWholeTree(t *testing.T) {
	bash := bashOrSkip(t)
	shrinkHeavyLimit(t, 8*time.Second)
	script, pidFile := chainScript(t)
	step := Step{Title: "install dependencies", Cmd: []string{bash, script, pidFile, "wait"}, Dir: t.TempDir(), Install: true}
	err := runStep(step, os.Environ(), io.Discard, io.Discard)
	requireChainEnded(t, pidFile, err)
}

func TestInstallDeps_LimitEndsTheInstallsWholeTree(t *testing.T) {
	bash := bashOrSkip(t)
	shrinkHeavyLimit(t, 8*time.Second)
	script, pidFile := chainScript(t)
	rule := depinstall.Rule{Argv: []string{bash, script, pidFile, "wait"}}
	err := installDeps(rule, t.TempDir(), io.Discard, io.Discard)
	requireChainEnded(t, pidFile, err)
}

func TestHeavyRun_KeepsTheGivenEnvironmentWholeAndUnsealed(t *testing.T) {
	bash := bashOrSkip(t)
	var out bytes.Buffer
	env := append(os.Environ(), "F10_STEP_MARK=given")
	err := heavyRun(childrun.Spec{Name: bash, Env: env, Stdout: &out, Args: []string{"-c", `printf '%s' "$F10_STEP_MARK"`}})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "given" {
		t.Fatalf("child saw %q, want the environment the caller built, as is", out.String())
	}
}

func TestHeavyRun_AFailingChildKeepsItsOwnExitStatusAndIsNotCalledATimeout(t *testing.T) {
	bash := bashOrSkip(t)
	err := heavyRun(childrun.Spec{Name: bash, Args: []string{"-c", "exit 3"}})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("err = %v, want the child's own exit status 3 and no timeout wording", err)
	}
}

func TestLightOutput_ChildNeverPromptsAndKeepsTheCallersEnvironment(t *testing.T) {
	bash := bashOrSkip(t)
	t.Setenv("F10_CALLER_MARK", "kept")
	out, err := lightOutput(childrun.Spec{Name: bash, Args: []string{"-c", `printf '%s|%s|%s' "$GIT_TERMINAL_PROMPT" "$GH_PROMPT_DISABLED" "$F10_CALLER_MARK"`}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "0|1|kept" {
		t.Fatalf("child saw %q, want a prompt-free child in the caller's own environment", out)
	}
}

func TestLightOutput_ReturnsStdoutAndTheExitErrorOfAFailingChild(t *testing.T) {
	bash := bashOrSkip(t)
	var errb bytes.Buffer
	out, err := lightOutput(childrun.Spec{Name: bash, Args: []string{"-c", "echo partial; echo why >&2; exit 4"}, Stderr: &errb})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("err = %v, want the exit status 4", err)
	}
	if string(out) != "partial\n" || errb.String() != "why\n" {
		t.Fatalf("stdout = %q, stderr = %q, want what the child wrote on each", out, errb.String())
	}
}

// The child is runtest's shell chain, which records the Windows pid of each of
// its three processes: a shell's own $$ is an MSYS pid, and asking the OS about
// it names some other process or none (a Windows pid is a multiple of 4, an
// MSYS pid is not), so a check of it passed or failed by what else was running.
func TestLightRun_TimeoutEndsTheChildAndSaysSo(t *testing.T) {
	bash := bashOrSkip(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "chain.sh")
	if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "pids")
	start := time.Now()
	err := lightRun(childrun.Spec{Name: bash, Args: []string{filepath.ToSlash(script), filepath.ToSlash(pidFile), "wait"}, Timeout: 10 * time.Second})
	if !errors.Is(err, childrun.ErrTimeout) {
		t.Fatalf("err = %v, want the timeout that ended the child", err)
	}
	if elapsed := time.Since(start); elapsed > 60*time.Second {
		t.Fatalf("a child given 10s held the caller for %s", elapsed)
	}
	runtest.RequireTreeGone(t, pidFile, 3)
}

func TestLaneHeadSHA_AFailureCarriesGitsOwnStderr(t *testing.T) {
	_, err := laneHeadSHA(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("err = %v, want git's own words about the directory, carried from its stderr", err)
	}
}
