package cli

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateStatus is `aphrollo gate status` (issue #430): read-only, prints
// what an inconclusive gate line now tells a session to go look at instead
// of rerunning into the same queue — the deferred edit jobs on this box and
// every global build slot's holder. --wait
// additionally blocks until THIS checkout's own deferred edit job, if any,
// reaches a verdict, and prints that verdict line verbatim instead of the
// report.
func runGateStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	wait := fs.Bool("wait", false, "block until this checkout's own deferred edit job has a verdict, then print it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := tdd.RepoRoot(".")
	if root == "" {
		root = "."
	}

	if *wait {
		advisory, ok := tdd.WaitDeferredEditJob(root)
		if !ok {
			fmt.Fprintln(stdout, "aphrollo gate status --wait: no deferred edit job recorded for this checkout")
			return 1
		}
		fmt.Fprintln(stdout, advisory)
		return 0
	}

	jobs := tdd.ActiveDeferredJobs()
	slots := tdd.SnapshotBuildSlots()
	waiters := tdd.QueueWaitersForRoot(root)
	fmt.Fprint(stdout, tdd.FormatGateStatus(jobs, slots, waiters, time.Now()))
	return 0
}
