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
	if j, ok := tdd.PostCommitHook(root); ok {
		fmt.Fprintf(stderr, "gate: mutation run started for %s (pid %d)\n", j.Branch, j.PID)
	}
	return 0
}

// isFlagSet reports whether the caller WROTE a flag, which is not the same
// question as whether its value is empty: `--diff ""` is a CI run with a base
// nobody computed, and answering it with the local job's behaviour would run
// the wrong thing rather than say so.
func isFlagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// runGateMutants dispatches the mutation job's own verbs. They are addressed
// by a job FILE rather than by flags because the wrapper is spawned detached:
// the description of the run has to outlive the process that decided it.
func runGateMutants(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "aphrollo gate mutants: expected a verb (run, go)")
		return 2
	}
	switch args[0] {
	case "run", "go":
		fs := flag.NewFlagSet("mutants "+args[0], flag.ContinueOnError)
		fs.SetOutput(stderr)
		job := fs.String("job", "", "path to the job file describing the run")
		var diff, receipt *string
		if args[0] == "go" {
			// CI's addressing: the merge base its diff is scoped to, and
			// where to leave the receipt for the workflow to upload. The
			// detached local job is addressed by a job FILE instead, because
			// it is spawned detached and the description of the run has to
			// outlive the process that decided it.
			diff = fs.String("diff", "", "merge base to scope the run to (CI: run in the foreground and judge)")
			receipt = fs.String("receipt", "", "where to write the signed receipt")
		}
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if args[0] == "go" {
			if isFlagSet(fs, "diff") {
				return tdd.RunGoMutantsCI(tdd.GoMutantsCI{BaseSHA: *diff, Receipt: *receipt}, stderr)
			}
			// The Go half of the detached job: gremlins over the lane diff,
			// writing the same receipt the Rust runner writes.
			return tdd.RunGoMutantsJob(*job)
		}
		return tdd.RunMutantsJob(*job)
	default:
		fmt.Fprintf(stderr, "aphrollo gate mutants: unknown verb %q (expected run or go)\n", args[0])
		return 2
	}
}
