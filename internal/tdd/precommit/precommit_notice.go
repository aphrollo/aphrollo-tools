package precommit

import (
	"fmt"
	"io"
	"time"
)

// startNoticeAfter is how long a check runs before the gate says it is
// running. A declared command can take minutes (a regen and the DB suites of
// every staged package); until it ends nothing else is printed, and a reader
// cannot tell it from a hung gate.
var startNoticeAfter = 15 * time.Second

// setStartNoticeAfter replaces the threshold and answers the restore. A test
// that calls it must not run in parallel.
func setStartNoticeAfter(d time.Duration) (restore func()) {
	prev := startNoticeAfter
	startNoticeAfter = d
	return func() { startNoticeAfter = prev }
}

// runNoticedTo runs r in root through run, and when it is still running after
// startNoticeAfter prints one line to w saying so, naming the stage and the
// command. The line is written before runNoticedTo returns, never after.
func runNoticedTo(w io.Writer, gateName, stage, root string, r Runner, run SuiteRunner) SuiteResult {
	// A caller that holds its lines back may keep this notice live.
	if n, ok := w.(interface{ noticeWriter() io.Writer }); ok {
		w = n.noticeWriter()
	}
	// The line is printed after the threshold, so its stamp is not the start.
	after := startNoticeAfter
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-time.After(after):
			fmt.Fprintf(w, "gate %s: %s in %s → running %s (started %.0fs ago) …\n", gateName, stage, root, cmdString(r), after.Seconds())
		case <-done:
		}
	}()
	res := run(r, root)
	close(done)
	<-finished
	return res
}
