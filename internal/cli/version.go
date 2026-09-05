package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// runVersion prints what the linker stamped into this binary (see
// internal/buildinfo and selfinstall.go's buildArgs), or says plainly that
// it was not stamped rather than printing a misleading empty commit. version
// takes no arguments at all, so any (including -h/--help) is a usage error.
func runVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: aphrollo version")
		return 2
	}
	commit, builtAt, stamped := buildinfo.Stamp()
	if !stamped {
		fmt.Fprintln(stdout, "aphrollo (unstamped)")
		return 0
	}
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	fmt.Fprintf(stdout, "aphrollo %s built %s\n", short, builtAt)
	return 0
}
