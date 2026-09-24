package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

// runWorkspaceMergeWait is `workspace merge --wait`. With PR numbers it is a
// serial queue run from anywhere in the repo; without, it waits for and merges
// the PR of the lane the caller stands in (or the <repo> <branch> it names).
// --dry prints the plan — PRs, lanes, head SHAs — and stops.
func runWorkspaceMergeWait(pos []string, into, method string, deleteBranch, dry bool, opts workspace.WaitOpts, stdout, stderr io.Writer) int {
	prs, queue := workspace.PRNumbers(pos)
	var (
		t     *workspace.Target
		items []workspace.QueueItem
		err   error
	)
	if queue {
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
	if queue {
		err = workspace.RunMergeQueue(t.MainRepo, items, method, deleteBranch, opts, stdout, stderr)
	} else {
		err = workspace.MergeWait(t, method, deleteBranch, opts, stdout, stderr)
	}
	// Housekeeping, best-effort, as the plain merge does: sweep landed lanes
	// other than the one the caller stands in. A queue sweeps even when it
	// stopped part-way, since the PRs before the stop did land.
	if err == nil || queue {
		tdd.PruneMergedLanesAfterMerge(t.MainRepo, t.Worktree, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}
