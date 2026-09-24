package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ciwhy"
)

const ciUsage = `usage: aphrollo ci why [<pr>|<run-id>|--main] [--workflow NAME] [--raw]

Explains why a pipeline run is red, read-only: one line per failed job, then
its failing Go tests with their assertion lines, its mutation survivors,
timeouts and unmeasured mutants, or its infrastructure cause (runner lost
communication, cancelled, timed out, job log not found); any other failure
shows the last 15 lines of the failed step.

  <pr>         the latest --workflow run on the PR's head commit
  <run-id>     that run (a number of 10000000 or more is a run id)
  (none)       the PR of the current branch
  --main       the latest --workflow run on main
  --workflow   the pipeline's workflow name (default Pipeline)
  --raw        print the run's failed-step log exactly as gh returns it
`

// runIDFloor splits a bare number into a PR or a run id: GitHub run ids are
// eleven digits and climbing, PR numbers stay far below this.
const runIDFloor = 10_000_000

// ghCallTimeout bounds one gh invocation; a job log is the largest read.
const ghCallTimeout = 90 * time.Second

// ciGh is the ci verb's seam onto gh; tests replace it with recorded answers.
var ciGh ciwhy.Gh = execGh

// execGh runs gh and returns its stdout, or, on failure, stdout and stderr
// together so the caller can name gh's own reason.
func execGh(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ghCallTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return append(stdout.Bytes(), stderr.Bytes()...), err
	}
	return stdout.Bytes(), nil
}

func runCI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, ciUsage)
		return 0
	}
	if len(args) == 0 || args[0] != "why" {
		fmt.Fprint(stderr, ciUsage)
		return 2
	}
	fs := flag.NewFlagSet("ci why", flag.ContinueOnError)
	fs.SetOutput(stderr)
	onMain := fs.Bool("main", false, "the latest run on main")
	raw := fs.Bool("raw", false, "print the failed-step log untouched")
	workflow := fs.String("workflow", "Pipeline", "the pipeline's workflow name")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	target := ciwhy.Target{Main: *onMain, Workflow: *workflow}
	switch {
	case fs.NArg() > 1 || (*onMain && fs.NArg() == 1):
		fmt.Fprint(stderr, "aphrollo ci why: give one of <pr>, <run-id> or --main\n\n"+ciUsage)
		return 2
	case fs.NArg() == 1:
		n, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil || n < 1 {
			fmt.Fprintf(stderr, "aphrollo ci why: %q is not a PR number or run id\n\n%s", fs.Arg(0), ciUsage)
			return 2
		}
		if n >= runIDFloor {
			target.Run = n
		} else {
			target.PR = int(n)
		}
	}
	ctx, cancel := commandContext()
	defer cancel()
	explain := ciwhy.Why
	if *raw {
		explain = ciwhy.Raw
	}
	if err := explain(ctx, ciGh, target, stdout); err != nil {
		fmt.Fprintf(stderr, "aphrollo ci why: %v\n", err)
		return 1
	}
	return 0
}
