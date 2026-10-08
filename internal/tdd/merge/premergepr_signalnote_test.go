package merge

import (
	"bytes"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

// A signal that ends the test binary through the handler leaves "FAIL pkg"
// with no failing test and no "exit status" (go test prints that only when
// the binary wrote nothing). The handler's own line goes to the run's log,
// which a test discards, so the exit must also say why on stderr.
func TestWatchPRGateSignals_NamesTheSignalAndExitCodeOnStderrBeforeExiting(t *testing.T) {
	injected := make(chan os.Signal, 1)
	origSource := prGateSignalChan
	prGateSignalChan = func() (chan os.Signal, func()) { return injected, func() {} }
	t.Cleanup(func() { prGateSignalChan = origSource })

	var stderr bytes.Buffer
	origErr := prGateSignalStderr
	prGateSignalStderr = &stderr
	t.Cleanup(func() { prGateSignalStderr = origErr })

	var noteBeforeExit string
	exited := make(chan int, 1)
	origExit := prGateSignalExit
	prGateSignalExit = func(code int) {
		noteBeforeExit = stderr.String()
		exited <- code
	}
	t.Cleanup(func() { prGateSignalExit = origExit })

	stop := watchPRGateSignals(func() {}, io.Discard)
	defer stop()
	injected <- syscall.SIGTERM
	if code := <-exited; code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit code = %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	want := "gate premerge: exiting 143 on signal terminated\n"
	if !strings.HasSuffix(noteBeforeExit, want) {
		t.Fatalf("stderr before the exit = %q, want it to end with %q", noteBeforeExit, want)
	}
}
