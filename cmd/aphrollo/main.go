// Command aphrollo is the umbrella CLI for first-party dev-env tooling. It
// moves deterministic developer operations (refactors, and more to come) out of
// the agent token stream into a fast, lossless binary.
package main

import (
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
