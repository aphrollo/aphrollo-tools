package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// A base branch with a merge queue takes no direct merge: GitHub refuses it,
// and a PR lands by being enqueued. The queue builds the PR onto the branch as
// it is at that moment, runs the repo's merge_group checks on that, and merges
// it when they pass.
//
// Every read and write here names the PR's BASE repository (repo, "owner/name";
// empty means origin), because a PR from a fork lives there, not in origin.

// HasMergeQueue reports whether base has a merge_queue rule in repo. REST, not
// GraphQL: a repo with no queue must keep merging where GraphQL is refused.
func (g *GitHub) HasMergeQueue(slug, base string) (bool, error) {
	owner, repo, err := g.splitRepo(slug)
	if err != nil {
		return false, err
	}
	key := owner + "/" + repo + "@" + base
	if queue, hit := queueRules.get(key); hit {
		return queue, nil
	}
	out, err := g.branchRules(owner, repo, base)
	if err != nil {
		return false, err
	}
	queue, err := RulesHaveMergeQueue(out)
	if err != nil {
		return false, err
	}
	queueRules.put(key, queue)
	return queue, nil
}

// branchRules reads the active rules that apply to a branch, every page.
func (g *GitHub) branchRules(owner, repo, branch string) ([]byte, error) {
	out, err := g.gh("api", "--paginate", "repos/"+owner+"/"+repo+"/rules/branches/"+url.PathEscape(branch))
	return RulesReadResult(branch, out, err)
}

// RulesReadResult turns the rules read's outcome into rules or a refusal. A 404
// means the host has no rulesets (an older GHES, a plan without them): there is
// no queue to be in, so it is an empty rule list. So is a 403 that says
// the plan lacks the feature (a private repository of a Free organisation).
// Any other 403 (a token without the scope, SSO) is not a claim about the plan. Anything else (auth, a 5xx,
// no network) is an error with the fix, because "no queue" is a claim.
func RulesReadResult(branch string, out []byte, err error) ([]byte, error) {
	if err == nil {
		return out, nil
	}
	text := strings.TrimSpace(string(out))
	if strings.Contains(text, "HTTP 404") || planLacksRulesets(text) {
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

var queueRules rulesCache

// ResetQueueRules forgets which branches have a queue; a test does, so one
// case's answer is not the next one's.
func ResetQueueRules() {
	queueRules.mu.Lock()
	defer queueRules.mu.Unlock()
	queueRules.m = nil
}

// RulesHaveMergeQueue reads a rules/branches answer, one list or a list of
// pages, for a merge_queue rule.
func RulesHaveMergeQueue(out []byte) (bool, error) {
	// --paginate prints one JSON list per page, back to back.
	dec := json.NewDecoder(bytes.NewReader(out))
	found := false
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			return found, nil
		}
		if err != nil {
			return false, fmt.Errorf("parsing the branch rules: %w", err)
		}
		found = found || hasQueueRule(v)
	}
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

// EnqueueArgs is gh's argv for the enqueue mutation. The head the PR was judged
// at goes in as expectedHeadOid, which GitHub checks itself: `gh pr merge
// --auto --match-head-commit` is not relied on to send it.
func EnqueueArgs(nodeID, sha string) []string {
	return []string{"api", "graphql", "-f", "id=" + nodeID, "-f", "sha=" + sha, "-f", "query=" + enqueueMutation}
}

// Enqueue puts a PR in the merge queue, bound to sha.
func (g *GitHub) Enqueue(slug string, pr int, sha string) error {
	owner, repo, err := g.splitRepo(slug)
	if err != nil {
		return err
	}
	id, err := g.gh("api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, pr), "--jq", ".node_id")
	if err != nil {
		return fmt.Errorf("gh api pulls/%d (node id): %v: %s", pr, err, strings.TrimSpace(string(id)))
	}
	out, err := g.gh(EnqueueArgs(strings.TrimSpace(string(id)), sha)...)
	if err == nil && strings.Contains(string(out), `"errors"`) {
		err = errors.New("GitHub refused the enqueue")
	}
	if err != nil {
		if HeadMovedText(out) {
			return host.HeadMoved(sha)
		}
		return fmt.Errorf("enqueue PR #%d: %v\n%s", pr, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// HeadMovedText reads a merge refusal as GitHub saying the PR head is no longer
// the commit the merge was bound to.
func HeadMovedText(out []byte) bool {
	return strings.Contains(strings.ToLower(string(out)), "head branch was modified")
}

// Merge merges the PR for branch, bound to Head: GitHub refuses it when the
// head is anything else by then, so a push between judgement and merge can
// never land a tree nobody judged. It deliberately does NOT pass
// `--delete-branch`: gh's branch deletion checks out the default branch first,
// and inside a worktree that switch fails because the main clone holds it.
func (g *GitHub) Merge(r host.MergeRequest) error {
	if r.UseBody {
		out, err := g.gh(MergeBodyArgs(r.Method, r.Subject, r.Body, r.Head, r.Branch)...)
		if err != nil {
			if HeadMovedText(out) {
				return host.HeadMoved(r.Head)
			}
			return fmt.Errorf("gh pr merge: %v\n%s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	owner, repo, err := g.ownerRepo()
	if err != nil {
		return err
	}
	n, err := g.number(r.Branch)
	if err != nil {
		return err
	}
	args := []string{"api", fmt.Sprintf("repos/%s/%s/pulls/%d/merge", owner, repo, n),
		"-X", "PUT", "-f", "merge_method=" + r.Method, "-f", "sha=" + r.Head}
	if r.Method != "rebase" {
		// A rebase writes no merge commit; the others get this verb's own
		// subject so GitHub's "Merge pull request #N from <branch>" never
		// carries the branch name into history.
		title, err := g.gh("api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, n), "--jq", ".title")
		if err != nil {
			return fmt.Errorf("gh api pulls title: %v: %s", err, strings.TrimSpace(string(title)))
		}
		args = append(args, "-f", "commit_title="+host.MergeSubject(string(title), n))
	}
	out, err := g.gh(args...)
	if err != nil {
		if HeadMovedText(out) {
			return host.HeadMoved(r.Head)
		}
		return fmt.Errorf("gh api pulls merge: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// MergeBodyArgs is gh's argv for a merge with an explicit subject and body,
// bound to the head sha that was judged.
func MergeBodyArgs(method, subject, body, sha, branch string) []string {
	return []string{"pr", "merge", "--" + method, "--subject", subject, "--body", body, "--match-head-commit", sha, "--", branch}
}

// DequeueHint names how to take a PR out of the merge queue by hand.
func (g *GitHub) DequeueHint(pr int) string {
	return fmt.Sprintf("dequeue it with `gh pr merge --disable-auto %d`, or the Remove from queue button on the PR page", pr)
}

// Dequeue takes a queue entry back out of the queue.
func (g *GitHub) Dequeue(entryID string) error {
	out, err := g.gh("api", "graphql", "-f", "id="+entryID,
		"-f", "query=mutation($id:ID!){dequeuePullRequest(input:{id:$id}){clientMutationId}}")
	if err != nil {
		return fmt.Errorf("dequeue %s: %v: %s", entryID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

const queueEntryQuery = `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){` +
	`pullRequest(number:$n){state mergeQueueEntry{id position state} mergeQueue{entries{totalCount}}}}}`

// QueueEntry reads where a PR stands in its merge queue: nil when it is in
// none. GraphQL is the only place a queue publishes positions.
func (g *GitHub) QueueEntry(slug string, pr int) (*host.QueueEntry, error) {
	owner, repo, err := g.splitRepo(slug)
	if err != nil {
		return nil, err
	}
	out, err := g.gh("api", "graphql", "-f", "owner="+owner, "-f", "name="+repo,
		"-F", "n="+strconv.Itoa(pr), "-f", "query="+queueEntryQuery)
	if err != nil {
		return nil, fmt.Errorf("gh api graphql (merge queue entry of #%d): %v: %s", pr, err, strings.TrimSpace(string(out)))
	}
	return ParseQueueEntry(out)
}

// ParseQueueEntry reads the queue entry query's answer: nil, nil for a PR in
// no queue.
func ParseQueueEntry(out []byte) (*host.QueueEntry, error) {
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
	e := &host.QueueEntry{ID: pr.MergeQueueEntry.ID, Position: pr.MergeQueueEntry.Position, State: pr.MergeQueueEntry.State}
	if pr.MergeQueue != nil {
		e.Total = pr.MergeQueue.Entries.TotalCount
	}
	return e, nil
}

const queueRemovalQuery = `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){pullRequest(number:$n){` +
	`autoMergeRequest{enabledAt} timelineItems(last:10,itemTypes:[ADDED_TO_MERGE_QUEUE_EVENT,REMOVED_FROM_MERGE_QUEUE_EVENT,AUTO_MERGE_DISABLED_EVENT]){` +
	`nodes{__typename ... on RemovedFromMergeQueueEvent{reason}}}}}}`

// QueueRemoval reads the queue events of a PR's timeline.
func (g *GitHub) QueueRemoval(slug string, pr int) (host.QueueRemoval, error) {
	owner, repo, err := g.splitRepo(slug)
	if err != nil {
		return host.QueueRemoval{}, err
	}
	out, err := g.gh("api", "graphql", "-f", "owner="+owner, "-f", "name="+repo,
		"-F", "n="+strconv.Itoa(pr), "-f", "query="+queueRemovalQuery)
	if err != nil {
		return host.QueueRemoval{}, fmt.Errorf("gh api graphql (queue events of #%d): %v: %s", pr, err, strings.TrimSpace(string(out)))
	}
	return ParseQueueRemoval(out)
}

// MergeGroupRun finds the newest merge_group run the queue made for a PR: 0
// when there is none. The queue names its branches
// gh-readonly-queue/<base>/pr-<n>-<sha>.
func (g *GitHub) MergeGroupRun(slug string, pr int) (int64, error) {
	owner, repo, err := g.splitRepo(slug)
	if err != nil {
		return 0, err
	}
	jq := fmt.Sprintf(`[.workflow_runs[] | select(.head_branch | contains("/pr-%d-"))] | sort_by(.id) | last | .id // empty`, pr)
	out, err := g.gh("api", "repos/"+owner+"/"+repo+"/actions/runs?event=merge_group&per_page=100", "--jq", jq)
	if err != nil {
		return 0, fmt.Errorf("gh api actions/runs (merge_group): %v: %s", err, strings.TrimSpace(string(out)))
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// ParseQueueRemoval reads the queue events query's answer. Only the newest
// queue event counts: a removal followed by a new enqueue is not a removal. A
// removal whose reason is "merged" is the queue finishing its job, not a drop.
func ParseQueueRemoval(out []byte) (host.QueueRemoval, error) {
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
		return host.QueueRemoval{}, fmt.Errorf("parsing the queue events: %w", err)
	}
	if len(resp.Errors) > 0 {
		return host.QueueRemoval{}, fmt.Errorf("queue events: %s", resp.Errors[0].Message)
	}
	pr := resp.Data.Repository.PullRequest
	if pr == nil {
		return host.QueueRemoval{}, fmt.Errorf("queue events: no such pull request")
	}
	r := host.QueueRemoval{AutoMerge: pr.AutoMergeRequest != nil}
	for _, n := range pr.TimelineItems.Nodes {
		if n.Type == "RemovedFromMergeQueueEvent" && n.Reason == "failed_checks" {
			r.FailedChecks = true
		}
	}
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

// planLacksRulesets reports a 403 whose body is GitHub saying the repository's
// plan has no rulesets, such as "Upgrade to GitHub Pro or make this repository
// public to enable this feature." Only that wording counts: a bare 403 or
// "Resource not accessible by integration" can be a token scope and stays a
// refusal.
func planLacksRulesets(text string) bool {
	if !strings.Contains(text, "HTTP 403") {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "upgrade to github") || strings.Contains(lower, "to enable this feature")
}
