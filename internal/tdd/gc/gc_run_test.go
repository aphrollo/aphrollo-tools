package gc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

func gcBashOrSkip(t *testing.T) string {
	t.Helper()
	bash := runtest.BashCommand()
	if bash == "" {
		t.Skip("no bash on this box") // skip-ok: the child is a shell, which a box may lack.
	}
	return bash
}

func TestGCLightOutput_AFailingChildKeepsItsExitCodeAndItsStdout(t *testing.T) {
	bash := gcBashOrSkip(t)
	out, err := gcLightOutput(run.Spec{Name: bash, Args: []string{"-c", "echo seen; exit 1"}})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("err = %v, want exit status 1: pgrep's no-match answer is read from it", err)
	}
	if string(out) != "seen\n" {
		t.Fatalf("stdout = %q, want %q", out, "seen\n")
	}
}

func TestGCLightCombined_CarriesStdoutAndStderrInOrder(t *testing.T) {
	bash := gcBashOrSkip(t)
	out, err := gcLightCombined(run.Spec{Name: bash, Args: []string{"-c", "echo one; echo two >&2"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "one\ntwo\n" {
		t.Fatalf("combined = %q, want both lines in order", out)
	}
}

// The child is runtest's shell chain, which records the Windows pid of each
// of its three processes: a shell's own $$ is an MSYS pid, and asking the OS
// about it names some other process or none.
func TestGCLightRun_TimeoutEndsTheChild(t *testing.T) {
	bash := gcBashOrSkip(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "chain.sh")
	if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "pids")
	start := time.Now()
	err := gcLightRun(run.Spec{Name: bash, Args: []string{filepath.ToSlash(script), filepath.ToSlash(pidFile), "wait"}, Timeout: 10 * time.Second})
	if !errors.Is(err, run.ErrTimeout) {
		t.Fatalf("err = %v, want the timeout that ended the child", err)
	}
	if elapsed := time.Since(start); elapsed > 60*time.Second {
		t.Fatalf("a child given 10s held the sweep for %s", elapsed)
	}
	runtest.RequireTreeGone(t, pidFile, 3)
}
