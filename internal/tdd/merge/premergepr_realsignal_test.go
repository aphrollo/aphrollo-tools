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

// prGateRealSignalReadyFD is the descriptor the child reports readiness on:
// the parent passes a pipe's write end as the child's first extra file, and
// the child writes its checkout dir there once it is inside GatePRMerge's
// signal-armed window. A child about to block forever (and then be killed by
// a real signal) cannot report either fact back any other way, and a pipe of
// its own keeps the report apart from the test framework's stdout.
const prGateRealSignalReadyFD = 3

// prGateRealSignalReadyBound and prGateRealSignalExitBound are outer bounds
// only. The parent waits on the child's own report, and on the pipe closing
// when the child dies first, so a loaded runner delays the test rather than
// failing it; the bounds exist so a wedged child cannot hang the package.
const (
	prGateRealSignalReadyBound = 4 * time.Minute
	prGateRealSignalExitBound  = 2 * time.Minute
)

// TestGatePRMerge_RealSIGTERMKillsOnlyTheSubprocess is the one proof in this
// package that a REAL OS signal reaches watchPRGateSignals' production path
// end to end (no injected channel, no overridden exit seam): it re-execs this
// test binary with a guard env var, waits for the child to report on a pipe
// that it is inside GatePRMerge's signal-armed window, sends the child a real
// SIGTERM, and checks the child's own exit code and that its throwaway
// checkout is gone. Run as a subprocess so a regression here — the handler
// never firing, or firing but never cleaning up — can kill only that child,
// never this package's own test binary the way
// TestGatePRMerge_KilledMidRunStillRemovesTheThrowawayCheckout's real
// self-signal used to (see the "second delivery" note on prGateSignalChan in
// premergepr_signal.go).
func TestGatePRMerge_RealSIGTERMKillsOnlyTheSubprocess(t *testing.T) {
	if os.Getenv(prGateRealSignalChildEnv) == "1" {
		runPRGateRealSignalChild(t)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), prGateRealSignalReadyBound+prGateRealSignalExitBound+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^TestGatePRMerge_RealSIGTERMKillsOnlyTheSubprocess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), prGateRealSignalChildEnv+"=1")
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("making the readiness pipe: %v", err)
	}
	defer readyR.Close()
	cmd.ExtraFiles = []*os.File{readyW} // the child's descriptor prGateRealSignalReadyFD
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		_ = readyW.Close()
		t.Fatalf("starting the child: %v", err)
	}
	// Only the child holds the write end now, so the read below ends at its
	// report or at its exit, whichever comes first.
	_ = readyW.Close()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	// stopChild kills the child and waits for it, so its output buffers are
	// complete and no longer written when a failure message reads them.
	stopChild := func() string {
		_ = cmd.Process.Kill()
		<-waitErr
		return "stdout:\n" + stdout.String() + "\nstderr:\n" + stderr.String()
	}

	ready := make(chan string, 1)
	go func() {
		defer close(ready)
		line, err := bufio.NewReader(readyR).ReadString('\n')
		if err == nil {
			ready <- strings.TrimSuffix(line, "\n")
		}
	}()

	var checkoutDir string
	select {
	case dir, ok := <-ready:
		if !ok {
			t.Fatalf("the child exited before reaching the signal-armed window\n%s", stopChild())
		}
		checkoutDir = dir
	case <-time.After(prGateRealSignalReadyBound):
		t.Fatalf("the child never reached the signal-armed window within %s\n%s", prGateRealSignalReadyBound, stopChild())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signalling the child: %v\n%s", err, stopChild())
	}

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
	case <-time.After(prGateRealSignalExitBound):
		t.Fatalf("the child did not exit within %s of a delivered SIGTERM — signal handling regressed\n%s", prGateRealSignalExitBound, stopChild())
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

	// The readiness pipe is this process's alone: git and every other
	// subprocess the run starts must not hold it open past this process.
	syscall.CloseOnExec(prGateRealSignalReadyFD)
	readyW := os.NewFile(prGateRealSignalReadyFD, "prgate-ready")
	run := func(r Runner, dir string) SuiteResult {
		if _, err := fmt.Fprintln(readyW, dir); err != nil {
			// The runner may not be on the test's goroutine, where t.Fatal
			// belongs; a crash closes the pipe, which the parent reads as the
			// child dying before the window.
			panic(fmt.Sprintf("reporting readiness to the parent: %v", err))
		}
		// The parent's delivered SIGTERM ends this process from inside
		// watchPRGateSignals' own handler goroutine (prGateSignalExit ==
		// os.Exit here, untouched) — this call is never meant to return.
		select {}
	}
	err := GatePRMerge(root, "", run, io.Discard)
	t.Fatalf("GatePRMerge returned instead of being terminated by the delivered signal (err=%v)", err)
}
