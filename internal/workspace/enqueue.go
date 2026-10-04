package workspace

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
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
// How a PR lands, queue or merge, and how the queue is read, lives in the host
// port (host.Land and the adapter under it). What is here is the verb's side:
// the seams over the port that a test drives, and the receipt.
//
// Every read and write names the PR's BASE repository (repo, "owner/name";
// empty means origin), because a PR from a fork lives there, not in origin.

// Enqueued is a PR GitHub's merge queue holds and has not merged.
type Enqueued struct {
	PR     int
	URL    string
	Branch string
	Base   string
	Repo   string
}

// ghHasMergeQueue is the seam over "does this base branch of repo have a merge queue".
var ghHasMergeQueue = func(wt, slug, base string) (bool, error) {
	return hostFor(wt).HasMergeQueue(slug, base)
}

// ghEnqueuePR is the seam over putting a PR in the merge queue, bound to sha.
var ghEnqueuePR = func(wt, slug string, pr int, sha string) error {
	return hostFor(wt).Enqueue(slug, pr, sha)
}

// ghDequeuePR takes a queue entry back out of the queue.
var ghDequeuePR = func(wt, entryID string) error {
	return hostFor(wt).Dequeue(entryID)
}

// ghQueueEntry reads where a PR stands in its merge queue: nil when it is in none.
var ghQueueEntry = func(wt, slug string, pr int) (*QueueEntry, error) {
	return hostFor(wt).QueueEntry(slug, pr)
}

// landing is the host as a landing sees it: each primitive is the seam of this
// package that a test drives, so the landing of the port runs over them.
type landing struct{ wt string }

var _ host.LandHost = landing{}

func (l landing) HasMergeQueue(slug, base string) (bool, error) {
	return ghHasMergeQueue(l.wt, slug, base)
}
func (l landing) Enqueue(slug string, pr int, sha string) error {
	return ghEnqueuePR(l.wt, slug, pr, sha)
}
func (l landing) QueueEntry(slug string, pr int) (*QueueEntry, error) {
	return ghQueueEntry(l.wt, slug, pr)
}
func (l landing) Dequeue(id string) error   { return ghDequeuePR(l.wt, id) }
func (l landing) DequeueHint(pr int) string { return dequeueHint(pr) }
func (l landing) PRByBranch(branch string) (*host.PR, error) {
	info, err := ghViewPR(l.wt, branch)
	if err != nil || info == nil {
		return nil, err
	}
	return &host.PR{Number: info.Number, URL: info.URL, State: info.State, HeadSHA: info.HeadSHA, BaseRef: info.BaseRef, BaseRepo: info.BaseRepo}, nil
}
func (l landing) QueueRemoval(string, int) (QueueRemoval, error) { return QueueRemoval{}, nil }
func (l landing) MergeGroupRun(string, int) (int64, error)       { return 0, nil }

// Merge is the merge the base branch takes when it has no queue: with the
// verb's own commit message when the repo asked for one, else GitHub's.
func (l landing) Merge(r host.MergeRequest) error {
	if r.UseBody {
		return ghMergePRBody(l.wt, r.Branch, r.Method, r.Subject, r.Body, r.Head)
	}
	return ghMergePR(l.wt, r.Branch, r.Method, r.Head)
}

// landOnBase puts the PR on its base branch by the way the base takes one (host.Land)
// and says what happened. It answers the queue entry when the PR is queued and
// has not merged, nil when it merged.
func (m *Merge) landOnBase(pr *PRInfo, head, base string, subject, body string, useBody bool, stdout io.Writer) (*Enqueued, error) {
	landed, err := host.Land(landing{m.Target.Worktree}, host.LandRequest{
		Branch: m.Target.Branch, PR: pr.Number, URL: pr.URL, Repo: pr.BaseRepo, Base: base,
		Head: head, Method: m.Method, Subject: subject, Body: body, UseBody: useBody,
	})
	if err != nil || !landed.Queued {
		return nil, err
	}
	q := &Enqueued{PR: pr.Number, URL: pr.URL, Branch: m.Target.Branch, Base: base, Repo: pr.BaseRepo}
	switch {
	case landed.Already:
		fmt.Fprintf(stdout, "PR #%d is already in the %s merge queue at %s: %s\n", pr.Number, base, landed.Entry, pr.URL)
	case landed.Entry == nil:
		fmt.Fprintf(stdout, "queued PR #%d in the %s merge queue (position not reported yet): %s\n", pr.Number, base, pr.URL)
	default:
		fmt.Fprintf(stdout, "queued PR #%d in the %s merge queue at %s: %s\n", pr.Number, base, landed.Entry, pr.URL)
	}
	return q, nil
}

// dequeueHint names how to take a PR out of the merge queue by hand.
func dequeueHint(pr int) string {
	return fmt.Sprintf("dequeue it with `gh pr merge --disable-auto %d`, or the Remove from queue button on the PR page", pr)
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
