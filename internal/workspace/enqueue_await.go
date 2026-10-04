package workspace

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ciwhy"
)

// ghQueueRemoval reads the queue events of a PR's timeline.
var ghQueueRemoval = func(wt, slug string, pr int) (QueueRemoval, error) {
	return hostFor(wt).QueueRemoval(slug, pr)
}

// ghMergeGroupRun finds the newest merge_group run the queue made for a PR:
// 0 when there is none.
var ghMergeGroupRun = func(wt, slug string, pr int) (int64, error) {
	return hostFor(wt).MergeGroupRun(slug, pr)
}

// explainMergeGroupRun prints why a run is red, the way `aphrollo ci why` does.
var explainMergeGroupRun = func(wt string, id int64, w io.Writer) error {
	return ciwhy.Why(hostUntil(wt, time.Now().Add(3*time.Minute)), ciwhy.Target{Run: id}, w)
}

// A PR out of the queue in one poll is not yet a PR removed from it: the poll's
// reads are not one snapshot, and GitHub shows the merge a moment after the
// entry is gone. A removal is declared only after the PR has been out for
// removalPolls polls in a row, the later ones graceSleep apart.
const (
	removalPolls = 4
	graceSleep   = 5 * time.Second
)

// awaitMerged waits until the queue has merged the PR. It prints one line per
// change of the PR's place, never one per poll, and fails with the queue's
// reason when the PR leaves it without merging, whether it was seen in the
// queue or dropped before the first poll saw it. After the merge it runs the
// steps a direct merge runs.
func (m *Merge) awaitMerged(q *Enqueued, o WaitOpts, stdout, stderr io.Writer) error {
	wt := m.Target.Worktree
	deadline := waitNow().Add(o.Timeout)
	last := ""
	out := 0 // polls in a row the PR has been out of the queue
	for {
		head, err := ghPRHead(wt, strconv.Itoa(q.PR))
		if err != nil {
			return err
		}
		switch strings.ToUpper(head.State) {
		case "MERGED":
			return m.landed(q.PR, q.URL, "merge queue", stdout, stderr)
		case "CLOSED":
			return fmt.Errorf("PR #%d for %s was closed without merging", q.PR, q.Branch)
		}
		entry, entryErr := ghQueueEntry(wt, q.Repo, q.PR)
		line := ""
		switch {
		case entryErr != nil:
			line = "merge queue position unreadable: " + entryErr.Error()
		case entry != nil:
			out = 0
			line = entry.String()
		default:
			out++
			rem, remErr := ghQueueRemoval(wt, q.Repo, q.PR)
			if remErr == nil && rem.Removed && out >= removalPolls {
				return m.removedFromQueue(q, rem.Reason)
			}
			switch {
			case remErr == nil && rem.AutoMerge:
				line = "auto-merge is on; waiting for the required checks before it joins the " + q.Base + " merge queue"
			case remErr == nil && rem.Removed:
				line = "out of the " + q.Base + " merge queue (" + rem.Reason + "); confirming"
			default:
				line = "not in the " + q.Base + " merge queue yet"
			}
		}
		if state := fmt.Sprintf("  [queue] PR #%d %s", q.PR, line); state != last {
			fmt.Fprintln(stdout, state)
			last = state
		}
		wait := o.Interval
		if out > 0 && out < removalPolls && graceSleep < wait {
			wait = graceSleep
		}
		if !waitNow().Add(wait).Before(deadline) {
			return fmt.Errorf("timed out after %v waiting for the %s merge queue to merge PR #%d (last: %s)", o.Timeout, q.Base, q.PR, line)
		}
		waitSleep(wait)
	}
}

// removedFromQueue is the failure for a PR the queue dropped: GitHub's reason,
// and the queue's own merge_group run, summarised the way `aphrollo ci why` does.
func (m *Merge) removedFromQueue(q *Enqueued, reason string) error {
	wt := m.Target.Worktree
	head := fmt.Sprintf("PR #%d for %s was removed from the merge queue without merging", q.PR, q.Branch)
	if reason != "" {
		head += " (" + reason + ")"
	}
	if reason == "failed_checks" {
		recordQueueRed(wt, "", q.PR)
	}
	id, err := ghMergeGroupRun(wt, q.Repo, q.PR)
	switch {
	case err != nil:
		return fmt.Errorf("%s (the merge_group run could not be read: %v)", head, err)
	case id == 0:
		return fmt.Errorf("%s: no merge_group run was found for it, so it was removed for another reason (a push to the PR, a dequeue, a conflict)", head)
	}
	var why strings.Builder
	if err := explainMergeGroupRun(wt, id, &why); err != nil {
		return fmt.Errorf("%s (merge_group run %d could not be summarised: %v)", head, id, err)
	}
	return fmt.Errorf("%s:\n%s", head, strings.TrimRight(why.String(), "\n"))
}
