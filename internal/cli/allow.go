package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateAllow waives a wall for the current session (`gate allow primary`);
// with no wall argument it lists every active waiver instead.
func runGateAllow(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printWaivers(stdout)
		return 0
	}
	if len(args) != 1 || args[0] != tdd.WallPrimary {
		fmt.Fprintln(stderr, "usage: aphrollo gate allow [primary]")
		return 2
	}
	msg, err := tdd.AllowWall(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "aphrollo gate allow: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, msg)
	return 0
}

// runGateRevoke restores a wall waived by allow (`gate revoke primary`); with
// no wall argument it lists every active waiver instead.
func runGateRevoke(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printWaivers(stdout)
		return 0
	}
	if len(args) != 1 || args[0] != tdd.WallPrimary {
		fmt.Fprintln(stderr, "usage: aphrollo gate revoke [primary]")
		return 2
	}
	msg, err := tdd.Revoke(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "aphrollo gate revoke: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, msg)
	return 0
}

// printWaivers renders every active waiver, one per line as
// "<wall> since <RFC3339> by <session>", or "no waivers" when there are none.
func printWaivers(stdout io.Writer) {
	ws := tdd.ListWaivers()
	if len(ws) == 0 {
		fmt.Fprintln(stdout, "no waivers")
		return
	}
	for _, w := range ws {
		fmt.Fprintf(stdout, "%s since %s by %s\n", w.Wall, w.Since.Format(time.RFC3339), w.Session)
	}
}
