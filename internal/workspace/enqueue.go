package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ciwhy"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A base branch with a merge queue takes no direct merge: GitHub refuses it, and
// a PR lands by being enqueued. The queue builds the PR onto the branch as it
// is at that moment, runs the repo's merge_group checks on that, and merges it
// when they pass. So under a queue the verb still judges everything it can
// judge on the PR itself (the lane is the PR head, the commits and text carry no
// tell, the PR's own checks are green on its head, the merged tree's laws), but
// it does not demand that a verdict is for the base as it stands: that is what
// the queue's own run is for.

// QueueEntry is where a queued PR stands: its place in the queue (1 is next to
// merge), how many PRs the queue holds, and GitHub's state word for the entry
// (QUEUED, AWAITING_CHECKS, MERGEABLE, UNMERGEABLE, LOCKED).
type QueueEntry struct {
	Position int
	Total    int
	State    string
}

func (e *QueueEntry) String() string {
	place := "position " + strconv.Itoa(e.Position)
	if e.Total > 0 {
		place += " of " + strconv.Itoa(e.Total)
	}
	if e.State == "" {
		return place
	}
	return place + " (" + strings.ToLower(strings.ReplaceAll(e.State, "_", " ")) + ")"
}

// Enqueued is a PR GitHub's merge queue holds and has not merged.
type Enqueued struct {
	PR     int
	URL    string
	Branch string
	Base   string
}

// ghHasMergeQueue is the seam over "does this base branch have a merge queue".
var ghHasMergeQueue = ghHasMergeQueueReal

// ghBranchRules reads the active rules that apply to a branch, as REST's
// rules/branches endpoint answers them. REST rather than GraphQL: a repo with
// no queue must keep merging where GraphQL is refused.
var ghBranchRules = func(wt, owner, repo, branch string) ([]byte, error) {
	out, err := ghCombinedOutput(wt, "api", "repos/"+owner+"/"+repo+"/rules/branches/"+url.PathEscape(branch))
	if err != nil {
		return nil, fmt.Errorf("gh api rules/branches/%s: %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// rulesCache remembers, for the life of the process, which branches have a
// queue: a multi-PR merge asks once per branch, not once per PR. Only an answer
// is kept; a failed read is asked again.
type rulesCache struct {
	mu sync.Mutex
	m  map[string]bool
}

func (c *rulesCache) get(key string) (queue, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	queue, ok = c.m[key]
	return queue, ok
}

func (c *rulesCache) put(key string, queue bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]bool{}
	}
	c.m[key] = queue
}

func (c *rulesCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = nil
}

var queueRulesCache rulesCache

// ghHasMergeQueueReal reports whether base has a merge_queue rule. A read that
// fails is an error: "no queue" is a claim, and a direct merge GitHub then
// refuses is a worse way to learn it was wrong.
func ghHasMergeQueueReal(wt, base string) (bool, error) {
	owner, repo, ok := githubOwnerRepo(wt)
	if !ok {
		return false, fmt.Errorf("origin is not a github remote in %s", wt)
	}
	key := owner + "/" + repo + "@" + base
	if queue, hit := queueRulesCache.get(key); hit {
		return queue, nil
	}
	out, err := ghBranchRules(wt, owner, repo, base)
	if err != nil {
		return false, err
	}
	queue, err := rulesHaveMergeQueue(out)
	if err != nil {
		return false, err
	}
	queueRulesCache.put(key, queue)
	return queue, nil
}

// rulesHaveMergeQueue reads a rules/branches answer for a merge_queue rule.
func rulesHaveMergeQueue(out []byte) (bool, error) {
	var rules []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(out, &rules); err != nil {
		return false, fmt.Errorf("parsing the branch rules: %w", err)
	}
	for _, r := range rules {
		if r.Type == "merge_queue" {
			return true, nil
		}
	}
	return false, nil
}

// enqueueArgs is gh's argv to put a PR in the merge queue, bound to the head
// that was judged: gh refuses when the PR head is another commit by then. With
// the PR's required checks green gh enqueues it; the queue, not this verb,
// picks the merge method.
func enqueueArgs(pr int, sha string) []string {
	return []string{"pr", "merge", "--auto", "--match-head-commit", sha, "--", strconv.Itoa(pr)}
}

// ghEnqueuePR is the seam over putting a PR in the merge queue.
var ghEnqueuePR = func(wt string, pr int, sha string) error {
	out, err := ghCombinedOutput(wt, enqueueArgs(pr, sha)...)
	if err != nil {
		if moved := headMoved(out, sha); moved != nil {
			return moved
		}
		return fmt.Errorf("gh pr merge --auto: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

const queueEntryQuery = `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){` +
	`pullRequest(number:$n){state mergeQueueEntry{position state} mergeQueue{entries{totalCount}}}}}`

// ghQueueEntry reads where a PR stands in its merge queue: nil when it is in
// none. GraphQL is the only place a queue publishes positions.
var ghQueueEntry = func(wt string, pr int) (*QueueEntry, error) {
	owner, repo, ok := githubOwnerRepo(wt)
	if !ok {
		return nil, fmt.Errorf("origin is not a github remote in %s", wt)
	}
	out, err := ghCombinedOutput(wt, "api", "graphql", "-f", "owner="+owner, "-f", "name="+repo,
		"-F", "n="+strconv.Itoa(pr), "-f", "query="+queueEntryQuery)
	if err != nil {
		return nil, fmt.Errorf("gh api graphql (merge queue entry of #%d): %v: %s", pr, err, strings.TrimSpace(string(out)))
	}
	return parseQueueEntry(out)
}

// parseQueueEntry reads queueEntryQuery's answer: nil, nil for a PR in no queue.
func parseQueueEntry(out []byte) (*QueueEntry, error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					MergeQueueEntry *struct {
						Position int    `json:"position"`
						State    string `json:"state"`
					} `json:"mergeQueueEntry"`
					MergeQueue *struct {
						Entries struct {
							TotalCount int `json:"totalCount"`
						} `json:"entries"`
					} `json:"mergeQueue"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing the merge queue entry: %w", err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("merge queue entry: %s", resp.Errors[0].Message)
	}
	pr := resp.Data.Repository.PullRequest
	if pr == nil {
		return nil, errors.New("merge queue entry: no such pull request")
	}
	if pr.MergeQueueEntry == nil {
		return nil, nil
	}
	e := &QueueEntry{Position: pr.MergeQueueEntry.Position, State: pr.MergeQueueEntry.State}
	if pr.MergeQueue != nil {
		e.Total = pr.MergeQueue.Entries.TotalCount
	}
	return e, nil
}

// ghMergeGroupRun finds the newest merge_group run the queue made for a PR:
// 0 when there is none. The queue names its branches gh-readonly-queue/<base>/pr-<n>-<sha>.
var ghMergeGroupRun = func(wt string, pr int) (int64, error) {
	jq := fmt.Sprintf(`[.workflow_runs[] | select(.head_branch | contains("/pr-%d-"))] | sort_by(.id) | last | .id // empty`, pr)
	out, err := ghCombinedOutput(wt, "api", "repos/{owner}/{repo}/actions/runs?event=merge_group&per_page=100", "--jq", jq)
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

// enqueue puts the PR in its base branch's merge queue, bound to head, and says
// where it stands. A PR already in the queue (a resumed run) is left as it is.
func (m *Merge) enqueue(pr *PRInfo, head, base string, stdout io.Writer) (*Enqueued, error) {
	wt := m.Target.Worktree
	q := &Enqueued{PR: pr.Number, URL: pr.URL, Branch: m.Target.Branch, Base: base}
	if entry, err := ghQueueEntry(wt, pr.Number); err == nil && entry != nil {
		fmt.Fprintf(stdout, "PR #%d is already in the %s merge queue at %s: %s\n", pr.Number, base, entry, pr.URL)
		return q, nil
	}
	if err := ghEnqueuePR(wt, pr.Number, head); err != nil {
		return nil, err
	}
	entry, err := ghQueueEntry(wt, pr.Number)
	if err != nil || entry == nil {
		fmt.Fprintf(stdout, "queued PR #%d in the %s merge queue (position not reported yet): %s\n", pr.Number, base, pr.URL)
		return q, nil
	}
	fmt.Fprintf(stdout, "queued PR #%d in the %s merge queue at %s: %s\n", pr.Number, base, entry, pr.URL)
	return q, nil
}

// awaitMerged waits until the queue has merged the PR. It prints one line per
// change of the PR's place, never one per poll, and fails with the queue's
// reason when the PR leaves it without merging. After the merge it runs the
// steps a direct merge runs.
func (m *Merge) awaitMerged(q *Enqueued, o WaitOpts, stdout, stderr io.Writer) error {
	wt := m.Target.Worktree
	deadline := waitNow().Add(o.Timeout)
	last := ""
	seenEntry := false
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
		entry, entryErr := ghQueueEntry(wt, q.PR)
		line := ""
		switch {
		case entryErr != nil:
			line = "merge queue position unreadable: " + entryErr.Error()
		case entry != nil:
			seenEntry = true
			line = entry.String()
		case seenEntry:
			// Gone from the queue and still open: removed. The merge can have
			// landed between this poll's two reads, so the state is read once more.
			again, err := ghPRHead(wt, strconv.Itoa(q.PR))
			if err != nil {
				return err
			}
			if strings.EqualFold(again.State, "MERGED") {
				return m.landed(q.PR, q.URL, "merge queue", stdout, stderr)
			}
			return m.removedFromQueue(q)
		default:
			line = "not in the " + q.Base + " merge queue yet"
		}
		if state := fmt.Sprintf("  [queue] PR #%d %s", q.PR, line); state != last {
			fmt.Fprintln(stdout, state)
			last = state
		}
		if !waitNow().Add(o.Interval).Before(deadline) {
			return fmt.Errorf("timed out after %v waiting for the %s merge queue to merge PR #%d (last: %s)", o.Timeout, q.Base, q.PR, line)
		}
		waitSleep(o.Interval)
	}
}

// removedFromQueue is the failure for a PR the queue dropped: the queue's own
// merge_group run says why, summarised the way `aphrollo ci why` does.
func (m *Merge) removedFromQueue(q *Enqueued) error {
	wt := m.Target.Worktree
	head := fmt.Sprintf("PR #%d for %s was removed from the merge queue without merging", q.PR, q.Branch)
	id, err := ghMergeGroupRun(wt, q.PR)
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

// recordQueued notes that the verb queued a PR it will not wait for, so the
// merge GitHub makes later is the verb's own when local trunk takes it in.
func (m *Merge) recordQueued(q *Enqueued) {
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: m.Target.Worktree, Verdict: "queued",
		Detail: map[string]string{"pr": strconv.Itoa(q.PR), "method": "merge queue"}})
}
