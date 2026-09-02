package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateStats is `aphrollo tdd stats`: one table of gate.log, so pipeline
// health is a number. Read-only — it never touches the log it reads.
func runGateStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "", "only count entries newer than this (e.g. 7d, 12h); default: the whole log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var cutoff time.Time
	if *since != "" {
		age, err := tdd.ParseGCAge(*since)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo tdd stats: %v\n", err)
			return 2
		}
		cutoff = time.Now().UTC().Add(-age)
	}

	path := tdd.GateLogPath()
	if path == "" {
		fmt.Fprintln(stderr, "aphrollo tdd stats: no state dir, so no gate log")
		return 1
	}
	// A log written by a newer binary may carry line shapes this one parses
	// wrong; a wrong tally is worse than no tally, so it says so and stops.
	if schema, newer := tdd.GateLogNewerSchema(); newer {
		fmt.Fprintf(stderr, "aphrollo tdd stats: gate.log is at schema %d, this binary reads %d — not counted\n",
			schema, tdd.StateSchema)
		return 1
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo tdd stats: %v\n", err)
		return 1
	}
	defer f.Close()

	fmt.Fprint(stdout, tdd.RenderGateStats(tdd.GateStats(f, cutoff)))
	return 0
}
