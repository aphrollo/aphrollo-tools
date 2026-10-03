package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

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
//
// Every read and write here names the PR's BASE repository (repo, "owner/name";
// empty means origin), because a PR from a fork lives there, not in origin.

// QueueEntry is where a queued PR stands: its place in the queue (1 is next to
// merge), how many PRs the queue holds, GitHub's state word for the entry
// (QUEUED, AWAITING_CHECKS, MERGEABLE, UNMERGEABLE, LOCKED) and its node id.
type QueueEntry struct {
	ID       string
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
	Repo   string
}

// splitRepo reads "owner/name", or origin's owner and name when slug is empty.
func splitRepo(wt, slug string) (owner, name string, ok bool) {
	if o, n, found := strings.Cut(slug, "/"); found && o != "" && n != "" {
		return o, n, true
	}
	return githubOwnerRepo(wt)
}

// ghHasMergeQueue is the seam over "does this base branch of repo have a merge queue".
var ghHasMergeQueue = ghHasMergeQueueReal

// ghBranchRules reads the active rules that apply to a branch, as REST's
// rules/branches endpoint answers them, every page. REST rather than GraphQL: a
// repo with no queue must keep merging where GraphQL is refused.
var ghBranchRules = func(wt, owner, repo, branch string) ([]byte, error) {
	out, err := ghCombinedOutput(wt, "api", "--paginate", "--slurp", "repos/"+owner+"/"+repo+"/rules/branches/"+url.PathEscape(branch))
	return rulesReadResult(branch, out, err)
}

// rulesReadResult turns the rules read's outcome into rules or a refusal. A 404
// means the host has no rulesets (an older GHES, a plan without them): there is
// no queue to be in, so it is an empty rule list. Anything else (auth, a 5xx, no
// network) is an error with the fix, because "no queue" is a claim.
func rulesReadResult(branch string, out []byte, err error) ([]byte, error) {
	if err == nil {
		return out, nil
	}
	text := strings.TrimSpace(string(out))
	if strings.Contains(text, "HTTP 404") {
		return []byte("[]"), nil
	}
	return nil, fmt.Errorf("gh api rules/branches/%s: %v: %s — check `gh auth status` and the network, then merge again", branch, err, text)
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

// ghHasMergeQueueReal reports whether base has a merge_queue rule in repo.
func ghHasMergeQueueReal(wt, slug, base string) (bool, error) {
	owner, repo, ok := splitRepo(wt, slug)
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

// rulesHaveMergeQueue reads a rules/branches answer, one list or a list of
// pages, for a merge_queue rule.
func rulesHaveMergeQueue(out []byte) (bool, error) {
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return false, fmt.Errorf("parsing the branch rules: %w", err)
	}
	return hasQueueRule(v), nil
}

func hasQueueRule(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if hasQueueRule(e) {
				return true
			}
		}
	case map[string]any:
		return x["type"] == "merge_queue"
	}
	return false
}

const enqueueMutation = `mutation($id:ID!,$sha:GitObjectID!){enqueuePullRequest(input:{pullRequestId:$id,expectedHeadOid:$sha}){` +
	`mergeQueueEntry{id position}}}`

// enqueueArgs is gh's argv for the enqueue mutation. The head the PR was judged
// at goes in as expectedHeadOid, which GitHub checks itself: `gh pr merge
// --auto --match-head-commit` is not relied on to send it.
func enqueueArgs(nodeID, sha string) []string {
	return []string{"api", "graphql", "-f", "id=" + nodeID, "-f", "sha=" + sha, "-f", "query=" + enqueueMutation}
}

// ghEnqueuePR is the seam over putting a PR in the merge queue, bound to sha.
var ghEnqueuePR = func(wt, slug string, pr int, sha string) error {
	owner, repo, ok := splitRepo(wt, slug)
	if !ok {
		return fmt.Errorf("origin is not a github remote in %s", wt)
	}
	id, err := ghCombinedOutput(wt, "api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, pr), "--jq", ".node_id")
	if err != nil {
		return fmt.Errorf("gh api pulls/%d (node id): %v: %s", pr, err, strings.TrimSpace(string(id)))
	}
	out, err := ghCombinedOutput(wt, enqueueArgs(strings.TrimSpace(string(id)), sha)...)
	if err == nil && strings.Contains(string(out), `"errors"`) {
		err = errors.New("GitHub refused the enqueue")
	}
	if err != nil {
		if moved := headMoved(out, sha); moved != nil {
			return moved
		}
		return fmt.Errorf("enqueue PR #%d: %v\n%s", pr, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ghDequeuePR takes a queue entry back out of the queue.
var ghDequeuePR = func(wt, entryID string) error {
	out, err := ghCombinedOutput(wt, "api", "graphql", "-f", "id="+entryID,
		"-f", "query=mutation($id:ID!){dequeuePullRequest(input:{id:$id}){clientMutationId}}")
	if err != nil {
		return fmt.Errorf("dequeue %s: %v: %s", entryID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

const queueEntryQuery = `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){` +
	`pullRequest(number:$n){state mergeQueueEntry{id position state} mergeQueue{entries{totalCount}}}}}`

// ghQueueEntry reads where a PR stands in its merge queue: nil when it is in
// none. GraphQL is the only place a queue publishes positions.
var ghQueueEntry = func(wt, slug string, pr int) (*QueueEntry, error) {
	owner, repo, ok := splitRepo(wt, slug)
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
						ID       string `json:"id"`
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
	e := &QueueEntry{ID: pr.MergeQueueEntry.ID, Position: pr.MergeQueueEntry.Position, State: pr.MergeQueueEntry.State}
	if pr.MergeQueue != nil {
		e.Total = pr.MergeQueue.Entries.TotalCount
	}
	return e, nil
}

// enqueue puts the PR in its base branch's merge queue, bound to head, and says
// where it stands. A PR already in the queue (a resumed run) is left as it is.
// The bind is GitHub's (expectedHeadOid); the head is read again right after, and
// a PR that moved anyway is taken back out and refused.
func (m *Merge) enqueue(pr *PRInfo, head, base string, stdout io.Writer) (*Enqueued, error) {
	wt := m.Target.Worktree
	q := &Enqueued{PR: pr.Number, URL: pr.URL, Branch: m.Target.Branch, Base: base, Repo: pr.BaseRepo}
	if entry, err := ghQueueEntry(wt, q.Repo, pr.Number); err == nil && entry != nil {
		fmt.Fprintf(stdout, "PR #%d is already in the %s merge queue at %s: %s\n", pr.Number, base, entry, pr.URL)
		return q, nil
	}
	if err := ghEnqueuePR(wt, q.Repo, pr.Number, head); err != nil {
		return nil, err
	}
	entry, entryErr := ghQueueEntry(wt, q.Repo, pr.Number)
	if now, err := ghViewPR(wt, m.Target.Branch); err == nil && now != nil && now.HeadSHA != "" && now.HeadSHA != head {
		msg := fmt.Sprintf("PR head moved to %s after it was judged and enqueued at %s", short(now.HeadSHA), short(head))
		if entryErr == nil && entry != nil {
			if err := ghDequeuePR(wt, entry.ID); err != nil {
				return nil, &JudgedHeadError{Msg: msg + fmt.Sprintf("; taking it out of the queue failed (%v) — dequeue it by hand", err)}
			}
		}
		return nil, &JudgedHeadError{Msg: msg + " — it was taken out of the queue; merge again to judge the new head"}
	}
	if entryErr != nil || entry == nil {
		fmt.Fprintf(stdout, "queued PR #%d in the %s merge queue (position not reported yet): %s\n", pr.Number, base, pr.URL)
		return q, nil
	}
	fmt.Fprintf(stdout, "queued PR #%d in the %s merge queue at %s: %s\n", pr.Number, base, entry, pr.URL)
	return q, nil
}

// scrubPRBody makes the PR body the one undercover judged, because the queue
// writes the squash message from the PR's title and body: a tell footer left
// in it would land in trunk, and a closing trailer only a lane commit carries
// would be lost. The body is read back and a rewrite that did not take refuses.
func (m *Merge) scrubPRBody(number int, title, want string, stdout io.Writer) error {
	wt := m.Target.Worktree
	_, raw, err := ghPRText(wt, m.Target.Branch)
	if err != nil {
		return err
	}
	same := func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) }
	if same(raw, want) || (strings.TrimSpace(raw) == "" && same(want, title)) {
		return nil
	}
	if err := ghEditPRBody(wt, m.Target.Branch, want); err != nil {
		return fmt.Errorf("refusing to enqueue %s: the PR body must be rewritten first and could not be: %w", m.Target.Branch, err)
	}
	_, again, err := ghPRText(wt, m.Target.Branch)
	if err != nil {
		return err
	}
	if !same(again, want) {
		return fmt.Errorf("refusing to enqueue %s: the PR body was rewritten but GitHub still holds the old one, and the queue would write it into main", m.Target.Branch)
	}
	fmt.Fprintf(stdout, "rewrote PR #%d's body for the queue: tell footer stripped, closing trailers kept\n", number)
	return nil
}

// recordQueued notes that the verb queued a PR it will not wait for, so the
// merge GitHub makes later is the verb's own when local trunk takes it in.
func (m *Merge) recordQueued(q *Enqueued) {
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: m.Target.Worktree, Verdict: "queued",
		Detail: map[string]string{"pr": strconv.Itoa(q.PR), "method": "merge queue"}})
}
