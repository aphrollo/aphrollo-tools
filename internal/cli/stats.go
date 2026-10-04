package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
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

	if core.StateRoot() == "" {
		fmt.Fprintln(stderr, "aphrollo tdd stats: no state dir, so no event log")
		return 1
	}
	// A log written by a newer binary may carry shapes this one reads wrong; a
	// wrong tally is worse than no tally, so it says so and stops.
	if schema, newer := core.EventsNewerSchema(); newer {
		fmt.Fprintf(stderr, "aphrollo tdd stats: the event log is at schema %d, this binary reads %d — not counted\n",
			schema, core.EventSchema)
		return 1
	}
	if schema, newer := tdd.GateLogNewerSchema(); newer {
		fmt.Fprintf(stderr, "aphrollo tdd stats: gate.log is at schema %d, this binary reads %d — not counted\n",
			schema, tdd.StateSchema)
		return 1
	}
	start, ok := core.GateHistoryStart()
	if !ok {
		fmt.Fprintln(stderr, "aphrollo tdd stats: no gate history yet: the event log holds no stage line")
		return 1
	}
	// The stage lines come from the repos' event logs, which keep 16 weeks, and
	// from gate.log for what was logged before the events carried them whole: a
	// window that opens before the oldest line says where the history starts.
	if !cutoff.IsZero() && start.After(cutoff) {
		fmt.Fprintf(stdout, "events since %s (the window opens %s: nothing older is kept)\n",
			start.UTC().Format("2006-01-02"), cutoff.UTC().Format("2006-01-02"))
	}
	fmt.Fprint(stdout, tdd.RenderGateStats(tdd.GateStats(strings.NewReader(tdd.GateLines(cutoff)), cutoff)))

	// The demotion trend is a THREE-WEEK question, so it re-reads the log
	// rather than riding on --since: a report narrowed to a day would
	// otherwise silently answer it with one day of data.
	fmt.Fprint(stdout, tdd.DemoteCandidateLines(tdd.DemoteCandidates(strings.NewReader(tdd.GateLines(time.Time{})), time.Now().UTC())))
	// Naming the candidates is as far as a REPORT goes. Opening the
	// false-positive issues for them is a write to somebody's tracker, and it
	// belongs to the verb that already talks to GitHub —
	// `aphrollo gate escape sync` — not to the command a human runs to read
	// the week's numbers.
	return 0
}
