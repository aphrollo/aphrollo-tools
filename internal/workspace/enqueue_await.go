package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ciwhy"
)

// QueueRemoval is what the PR's timeline says about leaving the merge queue:
// whether its latest queue event is a removal (and GitHub's reason, such as
// failed_checks), and whether auto-merge is still switched on for it.
type QueueRemoval struct {
	Removed   bool
	Reason    string
	AutoMerge bool
}

const queueRemovalQuery = `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){pullRequest(number:$n){` +
	`autoMergeRequest{enabledAt} timelineItems(last:10,itemTypes:[ADDED_TO_MERGE_QUEUE_EVENT,REMOVED_FROM_MERGE_QUEUE_EVENT,AUTO_MERGE_DISABLED_EVENT]){` +
	`nodes{__typename ... on RemovedFromMergeQueueEvent{reason}}}}}}`

// ghQueueRemoval reads the queue events of a PR's timeline.
var ghQueueRemoval = func(wt, slug string, pr int) (QueueRemoval, error) {
	owner, repo, ok := splitRepo(wt, slug)
	if !ok {
		return QueueRemoval{}, fmt.Errorf("origin is not a github remote in %s", wt)
	}
	out, err := ghCombinedOutput(wt, "api", "graphql", "-f", "owner="+owner, "-f", "name="+repo,
		"-F", "n="+strconv.Itoa(pr), "-f", "query="+queueRemovalQuery)
	if err != nil {
		return QueueRemoval{}, fmt.Errorf("gh api graphql (queue events of #%d): %v: %s", pr, err, strings.TrimSpace(string(out)))
	}
	return parseQueueRemoval(out)
}

// parseQueueRemoval reads queueRemovalQuery's answer. Only the newest queue
// event counts: a removal followed by a new enqueue is not a removal. A removal
// whose reason is "merged" is the queue finishing its job, not a drop.
func parseQueueRemoval(out []byte) (QueueRemoval, error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					AutoMergeRequest *struct{} `json:"autoMergeRequest"`
					TimelineItems    struct {
						Nodes []struct {
							Type   string `json:"__typename"`
							Reason string `json:"reason"`
						} `json:"nodes"`
					} `json:"timelineItems"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return QueueRemoval{}, fmt.Errorf("parsing the queue events: %w", err)
	}
	if len(resp.Errors) > 0 {
		return QueueRemoval{}, fmt.Errorf("queue events: %s", resp.Errors[0].Message)
	}
	pr := resp.Data.Repository.PullRequest
	if pr == nil {
		return QueueRemoval{}, fmt.Errorf("queue events: no such pull request")
	}
	r := QueueRemoval{AutoMerge: pr.AutoMergeRequest != nil}
	if n := len(pr.TimelineItems.Nodes); n > 0 {
		last := pr.TimelineItems.Nodes[n-1]
		switch last.Type {
		case "RemovedFromMergeQueueEvent":
			r.Removed, r.Reason = last.Reason != "merged", last.Reason
		case "AutoMergeDisabledEvent":
			r.Removed, r.Reason = true, "auto-merge disabled"
		}
	}
	return r, nil
}

// ghMergeGroupRun finds the newest merge_group run the queue made for a PR:
// 0 when there is none. The queue names its branches gh-readonly-queue/<base>/pr-<n>-<sha>.
var ghMergeGroupRun = func(wt, slug string, pr int) (int64, error) {
	owner, repo, ok := splitRepo(wt, slug)
	if !ok {
		return 0, fmt.Errorf("origin is not a github remote in %s", wt)
	}
	jq := fmt.Sprintf(`[.workflow_runs[] | select(.head_branch | contains("/pr-%d-"))] | sort_by(.id) | last | .id // empty`, pr)
	out, err := ghCombinedOutput(wt, "api", "repos/"+owner+"/"+repo+"/actions/runs?event=merge_group&per_page=100", "--jq", jq)
	if err != nil {
		return 0, fmt.Errorf("gh api actions/runs (merge_group): %v: %s", err, strings.TrimSpace(string(out)))
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// explainMergeGroupRun prints why a run is red, the way `aphrollo ci why` does.
var explainMergeGroupRun = func(wt string, id int64, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	gh := func(_ context.Context, args ...string) ([]byte, error) { return ghCombinedOutput(wt, args...) }
	return ciwhy.Why(ctx, gh, ciwhy.Target{Run: id}, w)
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
		m.recordQueueRed(q)
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
