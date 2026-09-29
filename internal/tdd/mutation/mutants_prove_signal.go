package mutation

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// A proof holds a disposable copy of the lane for the length of its run, and
// Go's default disposition for SIGINT, SIGTERM and SIGHUP ends the process at
// once with no deferred cleanup: a proof started in a background shell that
// was later killed would leave its copy on disk. This is the handler
// GatePRMerge arms over its throwaway checkout (#819), armed over the copy.

// proveSignals is every signal that ends the process while a proof holds
// its copy: Ctrl-C, a plain `kill`, and the hang-up a killed background
// shell sends its children.
var proveSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// proveSignalExit is the process-exit seam: a test replaces it so a
// delivered signal proves the cleanup ran without ending the test binary.
var proveSignalExit = os.Exit

// proveSignalChan is where watchProveSignals gets the channel it reads its
// one signal from. A test swaps in a channel it owns, so proving the
// handler's cleanup never sends the test binary a real signal.
var proveSignalChan = func() (ch chan os.Signal, stop func()) {
	ch = make(chan os.Signal, 1)
	signal.Notify(ch, proveSignals...)
	return ch, func() { signal.Stop(ch) }
}

// watchProveSignals arms the handler for the copy's lifetime: the first
// signal delivered while it is armed runs cleanup, then exits with the
// conventional 128+signal code. The returned stop disarms it; a caller defers
// stop AFTER deferring its own cleanup, so stop runs first on the normal
// return path and the handler can never race that cleanup. cleanup must be
// safe to call twice.
func watchProveSignals(cleanup func(), log io.Writer) (stop func()) {
	ch, stopSource := proveSignalChan()
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			fmt.Fprintf(log, "gate: mutants prove: %v — removing the proof's copy of the lane before exit\n", sig)
			cleanup()
			proveSignalExit(proveSignalExitCode(sig))
		case <-done:
		}
	}()
	return func() {
		// Stop first so no new signal can be queued, drain one that already
		// landed so it cannot fire after done is closed, then close done so
		// the goroutine exits either way.
		stopSource()
		select {
		case <-ch:
		default:
		}
		close(done)
	}
}

// proveSignalExitCode is the code a shell reports for a process a signal
// killed, so a caller reading the exit status sees the usual shape.
func proveSignalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 1
}
