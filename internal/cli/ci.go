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

const ciUsage = `usage: aphrollo ci run [--dry] [--ci-jobs N] [--ci-timeout DURATION]
       aphrollo ci why [<pr>|<run-id>|--main] [--workflow NAME] [--raw]

ci run is the one CI entry point: it runs the repo's own GitHub workflow(s)
that run on pull_request, in a throwaway worktree of this checkout's HEAD
merged into trunk. Every job's run: steps execute in needs order under bash
(Git Bash on Windows); uses: steps are not executed and are printed by name;
a matrix runs its first combination only, and the output says so. A job with
services or a container is skipped and named. Mutation is not run. A green is
stored per merge-result tree and reused. --dry prints the plan and runs
nothing.

Jobs run one at a time, in needs order, and every step runs below normal
priority (nice and ionice, BELOW_NORMAL_PRIORITY_CLASS on Windows), so a run on
a shared box takes what is left of it. --ci-jobs N (or ci-jobs in aphrollo.toml)
runs up to N independent jobs at once; their output is prefixed with the job's
name. --ci-timeout 45m (or ci-timeout) sets how long one step may run, 30m by
default; a step that reaches it is stopped and named in the job's result.

A run never changes this box's global toolchains: every install lands in a
scratch directory of its own (a python venv first on PATH, per-run npm, go and
cargo prefixes and caches), removed when the run ends and printed at its start.
A step that would change the box outside that (sudo, a system package manager,
pip install --user) is refused before it runs, naming the step.

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
	if len(args) > 0 && args[0] == "run" {
		return runCIRun(args[1:], stdout, stderr)
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
