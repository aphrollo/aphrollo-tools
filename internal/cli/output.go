package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateOutput is `aphrollo gate output`: the text of the run the gate
// itself last made for this repo root, header and all.
//
// It is the other half of the narrowing refusal. `gate stats` answers what
// the VERDICT was; nothing answered what the run PRINTED, so a session that
// needed one assertion line had only a hand rerun — which the rule refuses —
// and reworded commands are what followed. Read-only: it serves bytes the
// gate already captured and never runs anything itself.
//
// Lossless by contract: the record is written to stdout exactly as stored,
// unfiltered and untruncated (the 256 KB cap and its TAIL-kept truncation
// notice are applied once, at write time, and stated in the header).
func runGateOutput(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("output", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate output: %v\n", err)
		return 1
	}
	text, err := tdd.RetainedSuiteOutput(cwd)
	if err != nil {
		// One line, naming which of the two reasons it is — no record for
		// this root, or one too old to describe this tree — so a session
		// knows whether to wait for a gate run or to stop asking.
		fmt.Fprintf(stderr, "aphrollo gate output: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stdout, text); err != nil {
		fmt.Fprintf(stderr, "aphrollo gate output: %v\n", err)
		return 1
	}
	return 0
}
