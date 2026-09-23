package core

import (
	"os"
	"os/exec"
)

// silentStdio points a detached child's three streams at the null device and
// returns a closer for the handles. os/exec would open the null device for a
// nil stream anyway; doing it here says WHY out loud, because the reason is
// not obvious and getting it wrong is expensive: a stream left inheriting the
// session's console gives the child — and every compiler it starts — a
// console to write to and a window to show. Nothing detached has anywhere to
// print: the wrapper writes to its own log file, and the sweep is silent by
// design.
func silentStdio(cmd *exec.Cmd) func() {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
		return func() {}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	return func() { _ = null.Close() }
}
