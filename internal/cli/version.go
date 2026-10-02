package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// runVersion prints the semantic version this build carries and what the
// linker stamped into it (see internal/buildinfo and selfinstall.go's
// buildArgs), or says plainly that it was not stamped rather than printing a
// misleading empty commit. `version check` is the one subcommand: it holds a
// change to the version rule (version_check.go). Any other argument, including
// -h/--help, is a usage error.
func runVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "check" {
		return runVersionCheck(args[1:], stdout, stderr)
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: aphrollo version [check ...]")
		return 2
	}
	commit, builtAt, stamped := buildinfo.Stamp()
	if !stamped {
		fmt.Fprintf(stdout, "aphrollo %s (unstamped)\n", buildinfo.Version())
		return 0
	}
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	fmt.Fprintf(stdout, "aphrollo %s (%s built %s)\n", buildinfo.Version(), short, builtAt)
	return 0
}
