package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runPostCommit is the `gate postcommit` git hook: it starts the lane's
// mutation run and returns. It NEVER blocks and never reports failure — the
// commit has already happened by the time this runs, so a non-zero exit would
// only print a scary line about work that is safely landed.
func runPostCommit(stderr io.Writer) int {
	root := tdd.RepoRoot(".")
	if root == "" {
		return 0
	}
	if j, ok := tdd.StartMutantsJob(root); ok {
		fmt.Fprintf(stderr, "gate: mutation run started for %s (pid %d)\n", j.Branch, j.PID)
	}
	return 0
}

// runGateMutants dispatches the mutation job's own verbs. They are addressed
// by a job FILE rather than by flags because the wrapper is spawned detached:
// the description of the run has to outlive the process that decided it.
func runGateMutants(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aphrollo gate mutants: expected a verb (run)")
		return 2
	}
	switch args[0] {
	case "run":
		fs := flag.NewFlagSet("mutants run", flag.ContinueOnError)
		fs.SetOutput(stderr)
		job := fs.String("job", "", "path to the job file describing the run")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return tdd.RunMutantsJob(*job)
	default:
		fmt.Fprintf(stderr, "aphrollo gate mutants: unknown verb %q (expected run)\n", args[0])
		return 2
	}
}
