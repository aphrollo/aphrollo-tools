package gc

import (
	"errors"
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

func TestGCLightRun_TimeoutEndsTheChild(t *testing.T) {
	bash := gcBashOrSkip(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	err := gcLightRun(run.Spec{Name: bash, Args: []string{"-c", `echo $$ > "$0"; exec sleep 600`, filepath.ToSlash(pidFile)}, Timeout: 3 * time.Second})
	if !errors.Is(err, run.ErrTimeout) {
		t.Fatalf("err = %v, want the timeout that ended the child", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("a child given 3s held the sweep for %s", elapsed)
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
	if !runtest.AllGone(pids, 20*time.Second) {
		t.Errorf("the child %v outlived its timeout", pids)
	}
}
