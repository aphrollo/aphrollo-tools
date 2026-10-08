package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

// runWorkspaceMergeWait is `workspace merge --wait`. With PR numbers it is a
// serial queue run from anywhere in the repo; without, it waits for and merges
// the PR of the lane the caller stands in (or the <repo> <branch> it names).
// With resume it reloads the record of this repo's stopped queue and runs the
// PRs that queue left pending, refusing while its process still runs.
// --dry prints the plan — PRs, lanes, head SHAs — and stops.
func runWorkspaceMergeWait(pos []string, into, method string, deleteBranch, dry, resume bool, opts workspace.WaitOpts, stdout, stderr io.Writer) int {
	prs, queue := workspace.PRNumbers(pos)
	var (
		t     *workspace.Target
		prior *tdd.MergeQueueRecord
		items []workspace.QueueItem
		err   error
	)
	if resume {
		queue = true
		t, err = workspace.ResolveTarget("", "", "")
		if err == nil {
			prior, items, err = workspace.PlanResume(t.MainRepo)
		}
	} else if queue {
		t, err = workspace.ResolveTarget("", "", "")
		if err == nil {
			items, err = workspace.PlanMergeQueue(t.MainRepo, prs)
		}
	} else {
		var ok bool
		if t, ok = resolveVerbTarget(pos, into, stderr); !ok {
			return 2
		}
		items, err = workspace.PlanLane(t)
	}
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, workspace.RenderMergeQueue(items))
	if dry {
		return 0
	}
	// Every line from here on starts with the UTC time it began, so the gaps
	// between the wait's and the gate's stages can be read off the log.
	stampedOut, stampedErr := workspace.StampLines(stdout), workspace.StampLines(stderr)
	// The gate runs in this process and speaks on os.Stderr, so that goes
	// through the same stamp: its stage lines carry the prefix too.
	defer workspace.RouteProcessStderr(stampedErr)()
	if queue {
		err = workspace.ResumeMergeQueue(t.MainRepo, prior, items, method, deleteBranch, opts, stampedOut, stampedErr)
	} else {
		err = workspace.MergeWait(t, method, deleteBranch, opts, stampedOut, stampedErr)
	}
	// Housekeeping, best-effort, as the plain merge does: sweep landed lanes
	// other than the one the caller stands in. A queue sweeps even when it
	// stopped part-way, since the PRs before the stop did land.
	if err == nil || queue {
		tdd.PruneMergedLanesAfterMerge(t.MainRepo, t.Worktree, stampedOut, stampedErr)
		sweepAfterRun(t.MainRepo)
	}
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return mergeExitCode(err)
	}
	return 0
}

// mergeExitCode is the exit code of a merge that did not land: 2 when CI is
// green on the head but judged an older base, or when the lane is not the PR
// head the merge would judge (or the head moved after it was judged), so the
// operator rebases or pushes and runs the merge again, and 1 for any other
// refusal or failure.
func mergeExitCode(err error) int {
	if _, stale := tdd.AsStaleCIVerdict(err); stale {
		return 2
	}
	var judged *workspace.JudgedHeadError
	if errors.As(err, &judged) {
		return 2
	}
	return 1
}
