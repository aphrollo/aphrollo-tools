package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runPhase executes one detached build or run phase from its job record. It
// is spawned by the edit hook, never typed by a human, and reports through
// the job's log and result file rather than stdio — its parent is gone.
func runPhase(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("runphase", flag.ContinueOnError)
	fs.SetOutput(stderr)
	job := fs.String("job", "", "path to the deferred job record")
	if err := fs.Parse(args); err != nil {
		return 0
	}
	if *job == "" {
		fmt.Fprintln(stderr, "aphrollo tdd runphase: --job is required")
		return 0
	}
	return tdd.RunPhase(*job)
}
