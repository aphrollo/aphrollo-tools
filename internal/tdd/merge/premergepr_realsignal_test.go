//go:build !windows

package merge

// This file is built only where a real SIGTERM has POSIX delivery semantics.
// os.Process.Signal on windows implements only os.Kill and os.Interrupt;
// there is no windows equivalent of "send another process SIGTERM" for this
// proof to drive. The in-process test in premergepr_test.go still proves the
// same handler-runs-its-own-cleanup contract on every OS, windows included —
// this file adds nothing there, only the real-signal half of the proof.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// prGateRealSignalChildEnv turns this same test function into the child's
// own body instead of the parent's orchestration.
const prGateRealSignalChildEnv = "APHROLLO_TEST_PRGATE_REAL_SIGNAL_CHILD"

// prGateRealSignalReadyPrefix opens the one line the child prints once it is
// inside GatePRMerge's signal-armed window, followed by its checkout dir — a
// child about to block forever (and then be killed by a real signal) cannot
// report either fact back to its parent any other way.
const prGateRealSignalReadyPrefix = "PRGATE-REAL-SIGNAL-READY:"

// TestGatePRMerge_RealSIGTERMKillsOnlyTheSubprocess is the one proof in this
// package that a REAL OS signal reaches watchPRGateSignals' production path
// end to end (no injected channel, no overridden exit seam): it re-execs this
// test binary with a guard env var, waits for the child to report it is
// inside GatePRMerge's signal-armed window, sends the child a real SIGTERM,
// and checks the child's own exit code and that its throwaway checkout is
// gone. Run as a subprocess so a regression here — the handler never firing,
// or firing but never cleaning up — can kill only that child, never this
// package's own test binary the way
// TestGatePRMerge_KilledMidRunStillRemovesTheThrowawayCheckout's real
// self-signal used to (see the "second delivery" note on prGateSignalChan in
// premergepr_signal.go).
func TestGatePRMerge_RealSIGTERMKillsOnlyTheSubprocess(t *testing.T) {
	if os.Getenv(prGateRealSignalChildEnv) == "1" {
		runPRGateRealSignalChild(t)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^TestGatePRMerge_RealSIGTERMKillsOnlyTheSubprocess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), prGateRealSignalChildEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("wiring the child's stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the child: %v", err)
	}

	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if line, ok := strings.CutPrefix(sc.Text(), prGateRealSignalReadyPrefix); ok {
				ready <- line
				return
			}
		}
	}()

	var checkoutDir string
	select {
	case checkoutDir = <-ready:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the child never reached the signal-armed window\nstderr:\n%s", stderr.String())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signalling the child: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("want the child to exit with the delivered SIGTERM's conventional code, got: %v\nstderr:\n%s", err, stderr.String())
		}
		if got, want := exitErr.ExitCode(), 128+int(syscall.SIGTERM); got != want {
			t.Fatalf("child exit code = %d, want %d (128+SIGTERM — a bare kill-by-signal reports -1, meaning the handler never caught it)\nstderr:\n%s",
				got, want, stderr.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the child did not exit after a delivered SIGTERM within 20s — signal handling regressed")
	}

	if _, err := os.Stat(checkoutDir); !os.IsNotExist(err) {
		t.Fatalf("a real SIGTERM left the checkout behind: %s (stat err: %v)", checkoutDir, err)
	}
}

// runPRGateRealSignalChild is the child half: a real GatePRMerge run, neither
// exit seam overridden, blocked inside its first SuiteRunner call so the
// parent's delivered SIGTERM is what has to end it — production's actual
// os.Exit, never a test double.
func runPRGateRealSignalChild(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	writeMeasureBase(t, root)
	writeCrateSizeLaw(t, root, 15)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base and the law")
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "crates/a/src/other.rs", "pub fn other() -> i32 { 7 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane adds a small file")

	run := func(r Runner, dir string) SuiteResult {
		fmt.Println(prGateRealSignalReadyPrefix + dir)
		// The parent's delivered SIGTERM ends this process from inside
		// watchPRGateSignals' own handler goroutine (prGateSignalExit ==
		// os.Exit here, untouched) — this call is never meant to return.
		select {}
	}
	err := GatePRMerge(root, run, io.Discard)
	t.Fatalf("GatePRMerge returned instead of being terminated by the delivered signal (err=%v)", err)
}
