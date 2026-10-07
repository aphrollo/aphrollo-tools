package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// initThenPathLine runs the init under the new binary, unless noInit, and then
// says once what typing `aphrollo` runs: judged on the PATH a new shell gets,
// which the init has just converged, never on this process's own, which is
// the one it started with. When only this shell is behind, the line says to
// open a new one rather than asking for a PATH that is already right.
func initThenPathLine(root, prefix, bin string, forwarded []string, noInit bool, stdout, stderr io.Writer) int {
	code := 0
	if !noInit {
		code = initAfterSwap(prefix, bin, userSpaceInitArgs(root, forwarded), stdout, stderr)
	}
	next, here := userbin.PathCheckIn(root, userPathDirsFn()), userbin.PathCheck(root)
	switch {
	case next != "":
		fmt.Fprintf(stdout, "%s: %s\n", prefix, next)
	case here != "":
		fmt.Fprintf(stdout, "%s: this shell started before the PATH change and still runs another aphrollo; a new shell runs the install\n", prefix)
	}
	return code
}
