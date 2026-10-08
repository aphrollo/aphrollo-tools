// Command aphrollo is the umbrella CLI for first-party dev-env tooling. It
// moves deterministic developer operations (refactors, and more to come) out of
// the agent token stream into a fast, lossless binary.
package main

import (
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/cli"
	"github.com/aphrollo/aphrollo-tools/internal/handoff"
	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// main dispatches on the name it was invoked under before it looks at the
// arguments: the queue dir holds copies of this binary named cargo.exe and
// git.exe, and each runs the matching `gate` subcommand with the caller's own
// argv forwarded verbatim. A newer user-space install takes the verb first
// (internal/handoff), before stdin is read.
func main() {
	args := cli.DispatchArgs(os.Args[0], os.Args[1:])
	if root, err := userbin.Root(); err == nil {
		if code, handed := handoff.Maybe(args, buildinfo.Version(), root, os.Getenv, os.Stderr, handoff.Launch); handed {
			os.Exit(code)
		}
	}
	os.Exit(cli.Run(args, os.Stdin, os.Stdout, os.Stderr))
}
