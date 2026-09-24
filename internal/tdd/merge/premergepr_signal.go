package merge

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// prGateSignals is every OS signal that ends the process while GatePRMerge
// still holds its throwaway checkout: Ctrl-C, a plain `kill`, and the
// hang-up a killed background shell sends its children. Go's default
// disposition for all three is immediate termination with no deferred
// cleanup ever run, which is how a merge started in a background shell that
// was later killed left its checkout registered with nobody left to remove
// it.
var prGateSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// prGateSignalExit is the process-exit seam: overridden in a test so a
// delivered signal proves cleanup ran without killing the test binary.
var prGateSignalExit = os.Exit

// prGateSignalChan is where watchPRGateSignals gets the channel it reads its
// one signal from. Production arms a real OS channel; a test swaps this for
// a channel it owns and a no-op stop, so proving the handler's own cleanup
// runs never requires sending this test binary a real, process-wide signal.
// That matters because Mechanical calls the SuiteRunner more than once for a
// single GatePRMerge run (cargo fmt, then the workspace check, then the
// crate's own suite) — a test signalling itself on every one of those calls
// sends this binary two or three real SIGTERMs per run, and only the first
// is guaranteed to land while the handler goroutine below is still the one
// listening; the rest are extra, unproving noise racing whatever this
// process's own signal disposition is doing at that instant.
var prGateSignalChan = func() (ch chan os.Signal, stop func()) {
	ch = make(chan os.Signal, 1)
	signal.Notify(ch, prGateSignals...)
	return ch, func() { signal.Stop(ch) }
}

// watchPRGateSignals arms a handler for the checkout's lifetime: the first
// of prGateSignals delivered while it is armed runs cleanup exactly once,
// then exits with the conventional 128+signal code so a caller reading this
// process's exit status sees the same shape it would without this handler.
// The returned stop func disarms it; a caller defers stop BEFORE deferring
// its own cleanup so, on the normal return path, the signal handler is
// disarmed first and can never race the deferred cleanup running right
// after it.
func watchPRGateSignals(cleanup func(), log io.Writer) (stop func()) {
	ch, stopSource := prGateSignalChan()
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			fmt.Fprintf(log, "gate %s: %v — cleaning up the throwaway checkout before exit\n", premergeDisplayName, sig)
			cleanup()
			prGateSignalExit(prGateSignalExitCode(sig))
		case <-done:
		}
	}()
	return func() {
		// Stop first so no NEW signal can be queued, drain one that already
		// landed before this call so it cannot fire the handler after done is
		// closed, then close done so the goroutine exits either way.
		stopSource()
		select {
		case <-ch:
		default:
		}
		close(done)
	}
}

// prGateSignalExitCode is the exit code a shell reports for a process a
// signal killed (128+signal) — the conventional shape, so nothing reading
// this process's exit status sees anything unusual because this handler
// caught the signal instead of the runtime's own default disposition.
func prGateSignalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 1
}
