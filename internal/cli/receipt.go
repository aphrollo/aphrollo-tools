package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateReceipt is `aphrollo gate receipt <verb>`. The only verb is `sign`,
// and it is the ONLY thing on the box that writes a receipt's MAC: every
// runner signs through it, so "this receipt came from a run" is a fact the
// merge can check rather than a claim it has to take.
func runGateReceipt(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aphrollo gate receipt: expected a verb (sign)")
		return 2
	}
	if args[0] != "sign" {
		fmt.Fprintf(stderr, "aphrollo gate receipt: unknown verb %q (expected sign)\n", args[0])
		return 2
	}
	fs := flag.NewFlagSet("receipt sign", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outcomes := fs.String("outcomes", "", "the run's own mutants.out/outcomes.json, hashed into the receipt")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: aphrollo gate receipt sign [--outcomes <path>] <receipt.json>")
		return 2
	}
	path := fs.Arg(0)
	if err := tdd.SignReceiptFile(path, *outcomes); err != nil {
		fmt.Fprintf(stderr, "aphrollo gate receipt sign: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "signed %s\n", path)
	return 0
}
