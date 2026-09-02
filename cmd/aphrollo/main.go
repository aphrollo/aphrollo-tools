// Command aphrollo is the umbrella CLI for first-party dev-env tooling. It
// moves deterministic developer operations (refactors, and more to come) out of
// the agent token stream into a fast, lossless binary.
package main

import (
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/cli"
)

// main dispatches on the name it was invoked under before it looks at the
// arguments: the queue dir holds copies of this binary named cargo.exe and
// git.exe, and each runs the matching `gate` subcommand with the caller's own
// argv forwarded verbatim.
func main() {
	os.Exit(cli.Run(cli.DispatchArgs(os.Args[0], os.Args[1:]), os.Stdin, os.Stdout, os.Stderr))
}
