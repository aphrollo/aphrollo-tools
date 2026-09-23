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
// additionally blocks until the deferred edit job of this checkout — or of
// the directory named after the flags — reaches a verdict, and prints that
// verdict line verbatim instead of the report.
func runGateStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	wait := fs.Bool("wait", false, "block until the deferred edit job of this checkout, or of the tree named after the flags, has a verdict, then print it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// The tree to ask about is the one named, when one is: a BUILDING line
	// names the tree its job was recorded under, because the shell cwd is
	// wherever the harness last reset it — the primary checkout, which holds
	// none of a lane's jobs (issue #732).
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	root := tdd.RepoRoot(dir)
	if root == "" {
		root = dir
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
