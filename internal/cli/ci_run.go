package cli

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ciRunLocal is the seam over the local CI run, so the verb's own logic is
// tested without a repo and a suite.
var ciRunLocal = func(repo string, opts tdd.CIRunOptions, log io.Writer) (tdd.LocalCIVerdict, error) {
	return tdd.LocalCIWith(repo, log, opts)
}

// runCIRun is `ci run`: the single CI entry point. Local CI runs this same
// command, and a hosted workflow can wrap it.
func runCIRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "print the plan and stop (default: execute)")
	jobs := fs.String("ci-jobs", "", "jobs that may run at once (default: ci-jobs in aphrollo.toml, else 1: one at a time, in needs order)")
	timeout := fs.String("ci-timeout", "", "longest one step may run, such as 45m (default: ci-timeout in aphrollo.toml, else 30m)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "aphrollo ci run: unexpected argument %q\n\n%s", fs.Arg(0), ciUsage)
		return 2
	}
	opts, err := ciRunOptionsFrom(*jobs, *timeout)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ci run: %v\n", err)
		return 2
	}
	repo := tdd.RepoRoot(".")
	if repo == "" {
		fmt.Fprintln(stderr, "aphrollo ci run: not inside a git repository")
		return 2
	}
	if *dry {
		fmt.Fprintf(stdout, "ci run: would run the pull_request workflows of %s on HEAD merged into trunk, in a throwaway worktree (a stored green for the same tree is reused)\n", repo)
		return 0
	}
	v, err := ciRunLocal(repo, opts, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ci run: %v\n", err)
		return 1
	}
	switch {
	case v.Landed:
		fmt.Fprintln(stdout, "ci run: green — trunk already holds this HEAD, nothing to judge")
	case v.Reused:
		fmt.Fprintf(stdout, "ci run: green — merge result %s already judged, verdict reused\n", v.Tree)
	default:
		fmt.Fprintf(stdout, "ci run: green — merge result %s\n", v.Tree)
	}
	return 0
}

// ciRunOptionsFrom reads the two flags. A value that is not one is refused,
// never replaced by a default.
func ciRunOptionsFrom(jobs, timeout string) (tdd.CIRunOptions, error) {
	var o tdd.CIRunOptions
	if jobs != "" {
		n, err := strconv.Atoi(jobs)
		if err != nil || n < 1 {
			return tdd.CIRunOptions{}, fmt.Errorf("--ci-jobs %q is not a job count (want a whole number of 1 or more)", jobs)
		}
		o.Jobs = n
	}
	if timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil || d <= 0 {
			return tdd.CIRunOptions{}, fmt.Errorf("--ci-timeout %q is not a step time limit (want a duration above zero with a unit, such as 45m or 1h30m)", timeout)
		}
		o.StepTimeout = d
	}
	return o, nil
}
