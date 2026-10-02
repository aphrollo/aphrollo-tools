package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ciRunLocal is the seam over the local CI run, so the verb's own logic is
// tested without a repo and a suite.
var ciRunLocal = func(repo string, log io.Writer) (tdd.LocalCIVerdict, error) {
	return tdd.LocalCI(repo, tdd.RunSuite(tdd.DefaultPrecommitTimeout), log)
}

// runCIRun is `ci run`: the single CI entry point. Local CI runs this same
// command, and a hosted workflow can wrap it.
func runCIRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "print the plan and stop (default: execute)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "aphrollo ci run: unexpected argument %q\n\n%s", fs.Arg(0), ciUsage)
		return 2
	}
	repo := tdd.RepoRoot(".")
	if repo == "" {
		fmt.Fprintln(stderr, "aphrollo ci run: not inside a git repository")
		return 2
	}
	if *dry {
		fmt.Fprintf(stdout, "ci run: would judge HEAD of %s merged into trunk, in a throwaway worktree (a stored green for the same tree is reused)\n", repo)
		return 0
	}
	v, err := ciRunLocal(repo, stderr)
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
