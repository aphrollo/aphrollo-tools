package workspace

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// WaitOpts bounds `merge --wait`: how often it reads GitHub and how long it
// waits in total before giving up without merging.
type WaitOpts struct {
	Interval time.Duration
	Timeout  time.Duration
	CI       string // --ci mode (auto | local | github); empty reads the repo's setting
	// MethodSet is whether the operator named a merge method; behind a merge
	// queue it is ignored, and the verb says so.
	MethodSet bool
}

// DefaultWaitOpts polls every 30 s — the floor for a remote API on this box —
// for up to 90 minutes, longer than any CI run this repo has.
func DefaultWaitOpts() WaitOpts {
	return WaitOpts{Interval: 30 * time.Second, Timeout: 90 * time.Minute}
}

// PRHead is a PR as the wait reads it: which commit is its head right now.
type PRHead struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	State   string `json:"state"`
	HeadRef string `json:"headRefName"`
	HeadSHA string `json:"headRefOid"`
}

// ghPRHead reads a PR's number, state and head commit. ref is a branch or a PR
// number. A package var so the wait is driven by a scripted host in tests.
var ghPRHead = func(dir, ref string) (*PRHead, error) {
	p, err := hostFor(dir).PRByRef(ref)
	if err != nil {
		return nil, fmt.Errorf("gh api pr view %s: %w", ref, err)
	}
	if p == nil {
		return nil, fmt.Errorf("gh api pr view %s: no such pull request", ref)
	}
	return &PRHead{Number: p.Number, URL: p.URL, State: p.State, HeadRef: p.HeadRef, HeadSHA: p.HeadSHA}, nil
}

// ghChecksAt reads every check run and commit status on ONE commit, by SHA, so
// a result can never belong to a different head than the one asked about.
var ghChecksAt = func(dir, sha string) ([]CheckRun, error) {
	return hostFor(dir).ChecksAt(sha)
}

// laneHeadSHA is the commit the lane worktree has checked out — what the
// operator pushed and means to merge.
var laneHeadSHA = func(wt string) (string, error) {
	return wtHeadSHA(wt)
}

// listLanes lists the repo's linked worktrees; a seam so the queue finds lanes
// without a real repository in tests.
var listLanes = linkedWorktrees

// waitNow and waitSleep are the wait's clock, swapped for a fake in tests so
// no test ever sleeps.
var (
	waitNow   = time.Now
	waitSleep = time.Sleep
)

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// checkPassed classifies a concluded check. Anything not completed is still
// running, whatever its conclusion field says.
func checkPassed(c CheckRun) bool {
	switch strings.ToLower(c.Conclusion) {
	case "success", "neutral", "skipped":
		return true
	}
	return false
}

// pollState judges one poll: done is true when every check on the head passed;
// failed lists the failures; line is the state the operator sees.
//
// notStarted lists the jobs hosted CI never started; it is set only when no
// check really failed and none is still running, so a real red is always
// reported as one and the outage report is complete.
func pollState(head *PRHead, laneSHA string, checks []CheckRun) (line string, done bool, failed, notStarted []CheckRun) {
	if laneSHA != "" && head.HeadSHA != laneSHA {
		return fmt.Sprintf("PR head %s is not the lane's HEAD %s yet", short(head.HeadSHA), short(laneSHA)), false, nil, nil
	}
	var current []CheckRun
	for _, c := range checks {
		if c.SHA == head.HeadSHA {
			current = append(current, c)
		}
	}
	if len(current) == 0 {
		return "no check has started on this head yet", false, nil, nil
	}
	running := 0
	var idle []CheckRun
	for _, c := range current {
		switch {
		case !strings.EqualFold(c.Status, "completed"):
			running++
		case checkPassed(c):
		case c.NotStarted:
			idle = append(idle, c)
		default:
			failed = append(failed, c)
		}
	}
	switch {
	case len(failed) > 0:
		return fmt.Sprintf("%d of %d checks failed", len(failed), len(current)), false, failed, nil
	case running > 0:
		return fmt.Sprintf("%d of %d checks still running", running, len(current)), false, nil, nil
	case len(idle) > 0:
		return fmt.Sprintf("ci unavailable: %d of %d checks never started", len(idle), len(current)), false, nil, idle
	}
	skipped := 0
	for _, c := range current {
		if strings.EqualFold(c.Conclusion, "skipped") {
			skipped++
		}
	}
	if skipped == len(current) {
		return fmt.Sprintf("no check ran (all %d skipped)", skipped), true, nil, nil
	}
	return fmt.Sprintf("all %d checks passed", len(current)), true, nil, nil
}

// waitForGreen polls the lane's PR until every check on its current head has
// concluded. It returns nil only when all of them passed; a failed check, a
// PR that is no longer open, or the timeout is an error. It prints one line
// per state change, never one per poll.
func waitForGreen(t *Target, o WaitOpts, stdout io.Writer) error {
	deadline := waitNow().Add(o.Timeout)
	last := ""
	apart := 0 // polls in a row the lane has differed from the PR head
	rerequest := reRequests{asked: map[int64]bool{}}
	var reads netRetry
	for {
		head, err := ghPRHead(t.Worktree, t.Branch)
		if err != nil {
			if err := reads.pause(err, "the PR for "+t.Branch, t.Branch, o, deadline, stdout); err != nil {
				return err
			}
			continue
		}
		if !strings.EqualFold(head.State, "OPEN") {
			return fmt.Errorf("PR #%d for %s is %s, not open", head.Number, t.Branch, strings.ToLower(head.State))
		}
		laneSHA, err := laneHeadSHA(t.Worktree)
		if err != nil {
			return &JudgedHeadError{Msg: fmt.Sprintf("lane %s is not readable (%v) — run the merge from the lane that holds the PR's branch", t.Worktree, err)}
		}
		if laneSHA != "" && laneSHA != head.HeadSHA {
			// A push takes a moment to show as the PR's head, so a lane that
			// differs for a poll or two is waited for; one that still differs is
			// holding commits nobody is pushing, and the merge says so rather
			// than wait out the timeout for a head that will not come.
			apart++
			if apart >= laneSyncPolls {
				if err := laneAtHead(t.Worktree, head.HeadSHA, head.Number); err != nil {
					return err
				}
			}
		} else {
			apart = 0
		}
		checks, err := ghChecksAt(t.Worktree, head.HeadSHA)
		if err != nil {
			if err := reads.pause(err, fmt.Sprintf("PR #%d for %s", head.Number, t.Branch), strconv.Itoa(head.Number), o, deadline, stdout); err != nil {
				return err
			}
			continue
		}
		reads.reached()
		line, done, failed, notStarted := pollState(head, laneSHA, checks)
		if done {
			// Every check that exists passed, but a workflow run of this head that
			// is still queued (behind another run of its concurrency group) has made
			// none of its checks yet: the required one is coming, not missing.
			if open := requiredRunsOpen(t.Worktree, t.Branch, head, checks); open > 0 {
				line, done = fmt.Sprintf("required check not on this head yet: %d run(s) of the workflow that makes it still queued or running", open), false
			}
		}
		state := fmt.Sprintf("  [wait] PR #%d %s: %s", head.Number, short(head.HeadSHA), line)
		if state != last {
			fmt.Fprintln(stdout, state)
			last = state
		}
		if len(failed) > 0 {
			recordSettledCI(t.Worktree, head.HeadSHA, head.Number, "red", ciCause(checkNames(failed)))
			return failedChecksError(t.Branch, head, failed)
		}
		if len(notStarted) > 0 {
			if !reRequestUnacquired(t.Worktree, head, notStarted, &rerequest, stdout) {
				return notStartedError(t.Branch, head, notStarted)
			}
		}
		if done {
			recordSettledCI(t.Worktree, head.HeadSHA, head.Number, "green", "")
			return nil
		}
		if !waitNow().Add(o.Interval).Before(deadline) {
			return fmt.Errorf("timed out after %v waiting for PR #%d's checks (last: %s)", o.Timeout, head.Number, line)
		}
		waitSleep(o.Interval)
	}
}

// unfinishedRuns counts the PR's workflow runs on head that have not concluded.
// A run list that cannot be read counts none: the gate then judges the checks
// there are, as it did before the runs were looked at.
// requiredRunsOpen counts the PR's runs on head that have not concluded, of the
// workflow that makes the required check, when that check has not appeared among
// checks. A run of any other workflow says nothing about the required check, so
// it is never waited for here: its own checks are already in the poll. A run list
// that cannot be read counts none, and the gate then judges the checks there are.
func requiredRunsOpen(wt, branch string, head *PRHead, checks []CheckRun) int {
	check, workflow := tdd.CIBinding(wt)
	for _, c := range checks {
		if c.SHA == head.HeadSHA && (c.Name == check || strings.HasPrefix(c.Name, check+" (")) {
			return 0
		}
	}
	runs, err := ghPRRuns(wt, branch, head.Number)
	if err != nil {
		return 0
	}
	open := 0
	for _, r := range runs {
		if r.SHA == head.HeadSHA && !strings.EqualFold(r.Status, "completed") && path.Base(r.Path) == workflow {
			open++
		}
	}
	return open
}
func failedChecksError(branch string, head *PRHead, failed []CheckRun) error {
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to merge %s: %d check(s) failed on %s:", branch, len(failed), short(head.HeadSHA))
	for _, c := range failed {
		fmt.Fprintf(&b, "\n  %s  %s", c.Name, c.URL)
	}
	return fmt.Errorf("%s", b.String())
}

// notStartedError refuses the merge on an outage, naming it as one: the jobs
// listed never ran a step, so the code on this head was never judged.
func notStartedError(branch string, head *PRHead, idle []CheckRun) error {
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to merge %s: ci unavailable: jobs not started — %d check(s) on %s concluded without running a step "+
		"(hosted CI never ran them: a billing lock, a spending limit or no runner), so this head is untested, not red:",
		branch, len(idle), short(head.HeadSHA))
	for _, c := range idle {
		fmt.Fprintf(&b, "\n  %s  %s", c.Name, c.URL)
	}
	return &ciUnavailableError{msg: b.String()}
}

// MergeWait waits until every check on the lane PR's current head has
// concluded green, then merges it through Merge.Apply's steps — the same CI
// read, pre-merge gate and gh merge a plain `workspace merge` runs. On a base
// branch with a merge queue the PR is enqueued instead, and MergeWait then waits
// until the queue has merged it (or says why it did not).
func MergeWait(t *Target, method string, deleteBranch bool, o WaitOpts, stdout, stderr io.Writer) error {
	m, q, err := mergeWaitStart(t, method, deleteBranch, o, stdout, stderr)
	if err != nil || q == nil {
		return err
	}
	return m.awaitMerged(q, o, stdout, stderr)
}

// mergeWaitStart is MergeWait up to GitHub taking the PR: it waits for the
// head's checks, runs the gates, and then merges (a nil Enqueued) or enqueues
// the PR (the Enqueued it left in the queue, not yet merged).
func mergeWaitStart(t *Target, method string, deleteBranch bool, o WaitOpts, stdout, stderr io.Writer) (*Merge, *Enqueued, error) {
	m, err := MergePlan(t, method, deleteBranch)
	if err != nil {
		return nil, nil, err
	}
	m.CI, m.MethodSet = o.CI, o.MethodSet
	choice, err := chooseCI(t.Worktree, o.CI)
	if err != nil {
		return nil, nil, fmt.Errorf("refusing to merge %s: %w", t.Branch, err)
	}
	if choice.mode != tdd.CILocal {
		// Under auto an outage is not an answer: Apply reads the head's checks
		// once more and falls back to local CI, naming the outage as its reason.
		if err := waitForGreen(t, o, stdout); err != nil && (choice.mode != tdd.CIAuto || !isCIUnavailable(err)) {
			return nil, nil, err
		}
	}
	q, err := m.land(stdout, stderr)
	return m, q, err
}

// QueueItem is one PR of a merge queue: its head as planned and the lane
// worktree that holds its branch. Problem is set when the PR cannot be merged
// from here at all (no lane, not open, unreadable) — it is refused by name.
type QueueItem struct {
	PR      int
	Branch  string
	HeadSHA string
	Lane    string
	Problem string
}

// PRNumbers reads the positional args as a PR queue: every one must be a
// positive integer, else they are the <repo> <branch> form.
func PRNumbers(pos []string) ([]int, bool) {
	if len(pos) == 0 {
		return nil, false
	}
	nums := make([]int, 0, len(pos))
	for _, p := range pos {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, false
		}
		nums = append(nums, n)
	}
	return nums, true
}

// PlanMergeQueue resolves each PR's head branch and SHA and the lane worktree
// whose branch is that head. It reads GitHub once per PR and never waits.
func PlanMergeQueue(mainRepo string, prs []int) ([]QueueItem, error) {
	lanes, err := listLanes(mainRepo)
	if err != nil {
		return nil, err
	}
	items := make([]QueueItem, 0, len(prs))
	for _, n := range prs {
		it := QueueItem{PR: n}
		head, err := ghPRHead(mainRepo, strconv.Itoa(n))
		switch {
		case err != nil:
			it.Problem = err.Error()
		case !strings.EqualFold(head.State, "OPEN"):
			it.Branch, it.HeadSHA = head.HeadRef, head.HeadSHA
			it.Problem = "PR is " + strings.ToLower(head.State) + ", not open"
		default:
			it.Branch, it.HeadSHA = head.HeadRef, head.HeadSHA
			for _, l := range lanes {
				if l.Branch == head.HeadRef {
					it.Lane = l.Path
					break
				}
			}
			if it.Lane == "" {
				it.Problem = "no lane worktree has " + head.HeadRef + " checked out"
			}
		}
		items = append(items, it)
	}
	return items, nil
}

// PlanLane is the one-item plan for the lane the caller stands in: its PR,
// branch and head SHA, read once.
func PlanLane(t *Target) ([]QueueItem, error) {
	head, err := ghPRHead(t.Worktree, t.Branch)
	if err != nil {
		return nil, err
	}
	it := QueueItem{PR: head.Number, Branch: t.Branch, HeadSHA: head.HeadSHA, Lane: t.Worktree}
	if !strings.EqualFold(head.State, "OPEN") {
		it.Problem = "PR is " + strings.ToLower(head.State) + ", not open"
	}
	return []QueueItem{it}, nil
}

// RenderMergeQueue prints the plan: one line per PR, in queue order.
func RenderMergeQueue(items []QueueItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace merge --wait: %d PR(s), in order\n", len(items))
	for _, it := range items {
		lane := it.Lane
		if lane == "" {
			lane = "(no lane worktree)"
		}
		fmt.Fprintf(&b, "  #%d  %s  %s  %s\n", it.PR, it.Branch, short(it.HeadSHA), lane)
		if it.Problem != "" {
			fmt.Fprintf(&b, "      refused: %s\n", it.Problem)
		}
	}
	return b.String()
}

// queueStopError is the error a stopped queue returns: the whole report, with
// the refusal that stopped it kept underneath so its kind (a stale CI verdict,
// a lane that is not the PR head) still decides the exit code.
type queueStopError struct {
	msg string
	err error
}

func (e *queueStopError) Error() string { return e.msg }
func (e *queueStopError) Unwrap() error { return e.err }

// RunMergeQueue waits for and merges each PR in order, in one process. A PR
// refused at planning (no lane) is reported by name and skipped; a PR whose
// wait fails or whose merge is refused stops the queue, and the error names it
// and lists every PR left unattempted — the queue never runs ahead past one.
// It refuses to start while another live queue holds the repository, and
// keeps its record (mergequeue_record.go) while it runs.
func RunMergeQueue(mainRepo string, items []QueueItem, method string, deleteBranch bool, o WaitOpts, stdout, stderr io.Writer) error {
	return runQueue(mainRepo, nil, items, method, deleteBranch, o, stdout, stderr)
}

func runQueue(mainRepo string, prior *tdd.MergeQueueRecord, items []QueueItem, method string, deleteBranch bool, o WaitOpts, stdout, stderr io.Writer) error {
	rec, err := claimQueueRecord(mainRepo, prior, items, stderr)
	if err != nil {
		return err
	}
	var refused []string
	var inQueue []enqueuedPR
	for i, it := range items {
		if it.Problem != "" {
			fmt.Fprintf(stdout, "[refuse] PR #%d (%s): %s\n", it.PR, it.Branch, it.Problem)
			refused = append(refused, fmt.Sprintf("#%d", it.PR))
			settleQueuePR(rec, it.PR, tdd.MergeQueueRefused, stderr)
			continue
		}
		fmt.Fprintf(stdout, "PR #%d (%s) in %s\n", it.PR, it.Branch, it.Lane)
		t := &Target{Worktree: it.Lane, Branch: it.Branch, MainRepo: mainRepo, RepoName: filepath.Base(mainRepo)}
		m, q, err := mergeWaitStart(t, method, deleteBranch, o, stdout, stderr)
		if err != nil {
			settleQueuePR(rec, it.PR, tdd.MergeQueueRefused, stderr)
			var rest []string
			for _, r := range items[i+1:] {
				rest = append(rest, fmt.Sprintf("#%d", r.PR))
			}
			msg := fmt.Sprintf("queue stopped at PR #%d (%s): %v", it.PR, it.Branch, err)
			if len(rest) > 0 {
				msg += "\nnot attempted: " + strings.Join(rest, " ")
			}
			if len(inQueue) > 0 {
				msg += "\nalready in the merge queue, not waited for: " + strings.Join(prList(inQueue), " ")
			}
			return &queueStopError{msg: msg, err: err}
		}
		if q != nil {
			// In GitHub's merge queue, not merged yet: the next PR is enqueued
			// behind it, and every one is waited for once all are in.
			inQueue = append(inQueue, enqueuedPR{it: it, m: m, q: q})
			continue
		}
		settleQueuePR(rec, it.PR, tdd.MergeQueueMerged, stderr)
	}
	if err := awaitEnqueued(rec, inQueue, o, stdout, stderr); err != nil {
		return err
	}
	if len(refused) > 0 {
		return fmt.Errorf("refused: %s", strings.Join(refused, " "))
	}
	return nil
}

// enqueuedPR is a PR of a queue run that GitHub's merge queue holds.
type enqueuedPR struct {
	it QueueItem
	m  *Merge
	q  *Enqueued
}

func prList(prs []enqueuedPR) []string {
	var out []string
	for _, e := range prs {
		out = append(out, fmt.Sprintf("#%d", e.it.PR))
	}
	return out
}

// awaitEnqueued waits for each PR of a merge queue to be merged, in the order
// they were enqueued. One the queue drops does not stop the wait for the rest:
// they are in the queue, and each is reported as it ends. The error names every
// PR that did not merge and keeps the first failure's kind underneath.
func awaitEnqueued(rec *tdd.MergeQueueRecord, prs []enqueuedPR, o WaitOpts, stdout, stderr io.Writer) error {
	var failed []string
	var first error
	for _, e := range prs {
		if err := e.m.awaitMerged(e.q, o, stdout, stderr); err != nil {
			settleQueuePR(rec, e.it.PR, tdd.MergeQueueRefused, stderr)
			failed = append(failed, fmt.Sprintf("PR #%d (%s): %v", e.it.PR, e.it.Branch, err))
			if first == nil {
				first = err
			}
			continue
		}
		settleQueuePR(rec, e.it.PR, tdd.MergeQueueMerged, stderr)
	}
	if first == nil {
		return nil
	}
	return &queueStopError{msg: "merge queue did not merge every PR:\n" + strings.Join(failed, "\n"), err: first}
}

// maxReRequests is how many times a wait asks the host to run again the jobs
// hosted runners never acquired before it calls CI unavailable.
const maxReRequests = 2

// ghRerunFailed asks the host to run again the failed jobs of one Actions run.
var ghRerunFailed = func(dir string, run int64) error {
	return hostFor(dir).RerunFailedJobs(run)
}

// reRequests is what a wait has already asked for: how many asks it has made
// and which jobs they covered.
type reRequests struct {
	n     int
	asked map[int64]bool
	stale int // polls in a row that showed only jobs already asked for
}

// reRequestGrace is how many polls in a row a wait lets pass over jobs it has
// already asked for, which the list can lag in showing replaced, before it
// calls them an outage.
const reRequestGrace = 3

// reRequestUnacquired decides what a wait does about jobs that never started.
// It returns true when the wait should keep polling: the jobs are ones no
// hosted runner acquired and either they were asked for already (the list can
// still show the old job for a poll) or an ask was made now. It returns false
// when the outage is real: another cause, no asks left, or the host refused.
func reRequestUnacquired(dir string, head *PRHead, idle []CheckRun, rr *reRequests, stdout io.Writer) bool {
	fresh := false
	var runs []int64
	for _, c := range idle {
		id := host.RunID(c)
		if !c.NotAcquired || id == 0 {
			return false
		}
		if !rr.asked[c.ID] {
			fresh = true
		}
		if !slices.Contains(runs, id) {
			runs = append(runs, id)
		}
	}
	if !fresh {
		rr.stale++
		if rr.stale <= reRequestGrace {
			return true
		}
		fmt.Fprintf(stdout, "  [wait] PR #%d %s: no runner took %s after %d re-request(s); calling CI unavailable\n",
			head.Number, short(head.HeadSHA), jobNames(idle), rr.n)
		return false
	}
	rr.stale = 0
	if rr.n >= maxReRequests {
		return false
	}
	rr.n++
	for _, id := range runs {
		if err := ghRerunFailed(dir, id); err != nil {
			fmt.Fprintf(stdout, "  [wait] PR #%d %s: could not re-request run %d: %v\n", head.Number, short(head.HeadSHA), id, err)
			return false
		}
	}
	for _, c := range idle {
		rr.asked[c.ID] = true
	}
	fmt.Fprintf(stdout, "  [wait] PR #%d %s: hosted runners never took %d job(s); re-requesting them (ask %d of %d)\n",
		head.Number, short(head.HeadSHA), len(idle), rr.n, maxReRequests)
	return true
}

// jobNames lists the checks' names for a line a person reads.
func jobNames(checks []CheckRun) string {
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}
