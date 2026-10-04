package precommit

import (
	"fmt"
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

// runNoticed runs r in root through run, and when it is still running after
// startNoticeAfter prints one line saying so, naming the stage and the command.
// The line is written before runNoticed returns, never after.
func runNoticed(gateName, stage, root string, r Runner, run SuiteRunner) SuiteResult {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-time.After(startNoticeAfter):
			fmt.Fprintf(stderrFor(root), "gate %s: %s in %s → running %s …\n", gateName, stage, root, cmdString(r))
		case <-done:
		}
	}()
	res := run(r, root)
	close(done)
	<-finished
	return res
}
