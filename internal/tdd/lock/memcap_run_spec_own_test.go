package lock

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/run/runtest"
)

// specCaps are the two ways a slot child is held: not at all, and to a cap far
// above anything these tests allocate, which still puts the child behind the
// platform's enforcer (a scope or watchdog on unix, a job object on Windows).
var specCaps = map[string]MemCap{
	"uncapped": {},
	"capped":   {MB: 64 * 1024},
}

func bashOrSkip(t *testing.T) string {
	t.Helper()
	bash := runtest.BashCommand()
	if bash == "" {
		t.Skip("no bash on this box") // skip-ok: the child is a shell, which a box may lack.
	}
	return bash
}

func TestRunSpecCapped_ReportsTheChildsOwnExitCode(t *testing.T) {
	bash := bashOrSkip(t)
	for name, c := range specCaps {
		t.Run(name, func(t *testing.T) {
			res, err := RunSpecCapped(run.Spec{Name: bash, Args: []string{"-c", "exit 3"}}, c)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 3 {
				t.Fatalf("err = %v, want the child's exit status 3", err)
			}
			if res.Killed {
				t.Fatalf("a child that exited 3 by itself was reported as ended by the cap: %+v", res)
			}
		})
	}
}

func TestRunSpecCapped_PassesStdinAndCarriesOutputToTheWriters(t *testing.T) {
	bash := bashOrSkip(t)
	for name, c := range specCaps {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			spec := run.Spec{Name: bash, Args: []string{"-c", `read word; echo "out:$word"; echo "err:$word" >&2`},
				Stdin: bytes.NewReader([]byte("ping\n")), Stdout: &out, Stderr: &errOut}
			if _, err := RunSpecCapped(spec, c); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != "out:ping\n" {
				t.Fatalf("stdout = %q, want %q", got, "out:ping\n")
			}
			if got := errOut.String(); got != "err:ping\n" {
				t.Fatalf("stderr = %q, want %q", got, "err:ping\n")
			}
		})
	}
}

func TestRunSpecCapped_UncappedChildSeesTheCallersEnvironmentUnsealed(t *testing.T) {
	bash := bashOrSkip(t)
	var out bytes.Buffer
	spec := run.Spec{Name: bash, Args: []string{"-c", `printf %s "$F9_MARK"`},
		Env: append(os.Environ(), "F9_MARK=kept"), Stdout: &out}
	if _, err := RunSpecCapped(spec, MemCap{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "kept" {
		t.Fatalf("the child saw %q for F9_MARK, want the caller's environment taken as it is", out.String())
	}
}

// A slot child that outlives its timeout is ended with everything it started:
// the MSYS bash -> bash -> sleep chain is the shape `taskkill /T` misses.
func TestRunSpecCapped_TimeoutEndsAnMSYSShellChain(t *testing.T) {
	bash := bashOrSkip(t)
	for name, c := range specCaps {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "chain.sh")
			if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(dir, "pids")
			spec := run.Spec{Name: bash, Args: []string{filepath.ToSlash(script), filepath.ToSlash(pidFile), "wait"}, Timeout: 8 * time.Second}

			_, err := RunSpecCapped(spec, c)

			pids := runtest.ReadPids(pidFile)
			t.Cleanup(func() {
				for _, pid := range pids {
					runtest.ForceKill(pid)
				}
			})
			if !errors.Is(err, run.ErrTimeout) {
				t.Fatalf("err = %v, want the timeout that ended the chain", err)
			}
			if len(pids) < 3 {
				t.Fatalf("the chain recorded %d pids before the deadline, want 3: it never came up", len(pids))
			}
			if !runtest.AllGone(pids, 20*time.Second) {
				t.Errorf("a pid of the shell chain %v outlived the child that was ended at its timeout", pids)
			}
		})
	}
}

// A caller that gives up on a slot child ends everything it started, the shape
// a mutation run's patience takes: the context, not a timeout, is the limit.
func TestRunSpecCappedCtx_CancelEndsAnMSYSShellChain(t *testing.T) {
	bash := bashOrSkip(t)
	for name, c := range specCaps {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "chain.sh")
			if err := os.WriteFile(script, []byte(runtest.BashChain), 0o755); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(dir, "pids")
			spec := run.Spec{Name: bash, Args: []string{filepath.ToSlash(script), filepath.ToSlash(pidFile), "wait"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := RunSpecCappedCtx(ctx, spec, c)
				done <- err
			}()

			pids := runtest.WaitPids(pidFile, 3, 30*time.Second)
			cancel()
			t.Cleanup(func() {
				for _, pid := range pids {
					runtest.ForceKill(pid)
				}
			})
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a child its caller gave up on reported success")
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the run did not return within 30 s of its context ending")
			}
			if len(pids) < 3 {
				t.Fatalf("the chain recorded %d pids before the cancel, want 3: it never came up", len(pids))
			}
			if !runtest.AllGone(pids, 20*time.Second) {
				t.Errorf("a pid of the shell chain %v outlived the run its caller gave up on", pids)
			}
		})
	}
}

func TestRunMutationSpec_RunsTheChildUnderTheMutationCapAndAnswersItsExit(t *testing.T) {
	bash := bashOrSkip(t)
	_, err := RunMutationSpec(context.Background(), run.Spec{Name: bash, Args: []string{"-c", "exit 5"}}, t.TempDir(), 1)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 5 {
		t.Fatalf("err = %v, want the child's exit status 5", err)
	}
}
