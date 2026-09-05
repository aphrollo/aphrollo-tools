package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
)

// runVersion prints what the linker stamped into this binary (see
// internal/buildinfo and selfinstall.go's buildArgs), or says plainly that
// it was not stamped rather than printing a misleading empty commit.
func runVersion(stdout io.Writer) int {
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
