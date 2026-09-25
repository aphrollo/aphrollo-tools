package workspace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WaitOpts bounds `merge --wait`: how often it reads GitHub and how long it
// waits in total before giving up without merging.
type WaitOpts struct {
	Interval time.Duration
	Timeout  time.Duration
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

// CheckRun is one check (or legacy commit status) on one commit. SHA is the
// commit it ran on: a check whose SHA is not the PR's current head is a
// leftover from an older push and counts as not started.
type CheckRun struct {
	Name       string `json:"name"`
	SHA        string `json:"head_sha"`
	Status     string `json:"status"`     // queued | in_progress | completed
	Conclusion string `json:"conclusion"` // success | failure | … once completed
	URL        string `json:"html_url"`
}

// ghPRHead reads a PR's number, state and head commit. ref is a branch or a PR
// number. A package var so the wait is driven by a scripted gh in tests.
var ghPRHead = func(dir, ref string) (*PRHead, error) {
	out, err := ghCombinedOutput(dir, "pr", "view", "--json", "number,url,state,headRefName,headRefOid", "--", ref)
	if err != nil {
		return nil, fmt.Errorf("gh pr view %s: %v: %s", ref, err, strings.TrimSpace(string(out)))
	}
	var h PRHead
	if err := json.Unmarshal(out, &h); err != nil {
		return nil, fmt.Errorf("parsing gh pr view %s: %w", ref, err)
	}
	return &h, nil
}

// ghChecksAt reads every check run and commit status on ONE commit, by SHA, so
// a result can never belong to a different head than the one asked about.
// Each record is one JSON object per line, so a name with spaces stays whole.
var ghChecksAt = func(dir, sha string) ([]CheckRun, error) {
	runs, err := ghJSONLines(dir, "api", "--paginate", "repos/{owner}/{repo}/commits/"+sha+"/check-runs",
		"--jq", `.check_runs[] | {name, head_sha, status, conclusion, html_url}`)
	if err != nil {
		return nil, err
	}
	statuses, err := ghJSONLines(dir, "api", "repos/{owner}/{repo}/commits/"+sha+"/status",
		"--jq", `.sha as $s | .statuses[] | {name: .context, head_sha: $s, `+
			`status: (if .state == "pending" then "in_progress" else "completed" end), `+
			`conclusion: .state, html_url: .target_url}`)
	if err != nil {
		return nil, err
	}
	return append(runs, statuses...), nil
}

func ghJSONLines(dir string, args ...string) ([]CheckRun, error) {
	out, err := ghCombinedOutput(dir, args...)
	if err != nil {
		return nil, fmt.Errorf("gh %s: %v: %s", strings.Join(args[:2], " "), err, strings.TrimSpace(string(out)))
	}
	var rows []CheckRun
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r CheckRun
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("parsing gh %s: %w", strings.Join(args[:2], " "), err)
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

// laneHeadSHA is the commit the lane worktree has checked out — what the
// operator pushed and means to merge.
var laneHeadSHA = func(wt string) (string, error) {
	cmd := exec.Command("git", "-C", wt, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg = string(ee.Stderr)
		}
		return "", fmt.Errorf("git rev-parse HEAD in %s: %v: %s", wt, err, strings.TrimSpace(msg))
	}
	return strings.TrimSpace(string(out)), nil
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
func pollState(head *PRHead, laneSHA string, checks []CheckRun) (line string, done bool, failed []CheckRun) {
	if laneSHA != "" && head.HeadSHA != laneSHA {
		return fmt.Sprintf("PR head %s is not the lane's HEAD %s yet", short(head.HeadSHA), short(laneSHA)), false, nil
	}
	var current []CheckRun
	for _, c := range checks {
		if c.SHA == head.HeadSHA {
			current = append(current, c)
		}
	}
	if len(current) == 0 {
		return "no check has started on this head yet", false, nil
	}
	running := 0
	for _, c := range current {
		switch {
		case !strings.EqualFold(c.Status, "completed"):
			running++
		case !checkPassed(c):
			failed = append(failed, c)
		}
	}
	switch {
	case len(failed) > 0:
		return fmt.Sprintf("%d of %d checks failed", len(failed), len(current)), false, failed
	case running > 0:
		return fmt.Sprintf("%d of %d checks still running", running, len(current)), false, nil
	}
	return fmt.Sprintf("all %d checks passed", len(current)), true, nil
}

// waitForGreen polls the lane's PR until every check on its current head has
// concluded. It returns nil only when all of them passed; a failed check, a
// PR that is no longer open, or the timeout is an error. It prints one line
// per state change, never one per poll.
func waitForGreen(t *Target, o WaitOpts, stdout io.Writer) error {
	deadline := waitNow().Add(o.Timeout)
	last := ""
	for {
		head, err := ghPRHead(t.Worktree, t.Branch)
		if err != nil {
			return err
		}
		if !strings.EqualFold(head.State, "OPEN") {
			return fmt.Errorf("PR #%d for %s is %s, not open", head.Number, t.Branch, strings.ToLower(head.State))
		}
		laneSHA, err := laneHeadSHA(t.Worktree)
		if err != nil {
			return err
		}
		checks, err := ghChecksAt(t.Worktree, head.HeadSHA)
		if err != nil {
			return err
		}
		line, done, failed := pollState(head, laneSHA, checks)
		state := fmt.Sprintf("  [wait] PR #%d %s: %s", head.Number, short(head.HeadSHA), line)
		if state != last {
			fmt.Fprintln(stdout, state)
			last = state
		}
		if len(failed) > 0 {
			return failedChecksError(t.Branch, head, failed)
		}
		if done {
			return nil
		}
		if !waitNow().Add(o.Interval).Before(deadline) {
			return fmt.Errorf("timed out after %v waiting for PR #%d's checks (last: %s)", o.Timeout, head.Number, line)
		}
		waitSleep(o.Interval)
	}
}

func failedChecksError(branch string, head *PRHead, failed []CheckRun) error {
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to merge %s: %d check(s) failed on %s:", branch, len(failed), short(head.HeadSHA))
	for _, c := range failed {
		fmt.Fprintf(&b, "\n  %s  %s", c.Name, c.URL)
	}
	return fmt.Errorf("%s", b.String())
}

// MergeWait waits until every check on the lane PR's current head has
// concluded green, then merges it through Merge.Apply — the same CI read,
// pre-merge gate and gh merge a plain `workspace merge` runs.
func MergeWait(t *Target, method string, deleteBranch bool, o WaitOpts, stdout, stderr io.Writer) error {
	m, err := MergePlan(t, method, deleteBranch)
	if err != nil {
		return err
	}
	if err := waitForGreen(t, o, stdout); err != nil {
		return err
	}
	return m.Apply(stdout, stderr)
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

// RunMergeQueue waits for and merges each PR in order, in one process. A PR
// refused at planning (no lane) is reported by name and skipped; a PR whose
// wait fails or whose merge is refused stops the queue, and the error names it
// and lists every PR left unattempted — the queue never runs ahead past one.
func RunMergeQueue(mainRepo string, items []QueueItem, method string, deleteBranch bool, o WaitOpts, stdout, stderr io.Writer) error {
	var refused []string
	for i, it := range items {
		if it.Problem != "" {
			fmt.Fprintf(stdout, "[refuse] PR #%d (%s): %s\n", it.PR, it.Branch, it.Problem)
			refused = append(refused, fmt.Sprintf("#%d", it.PR))
			continue
		}
		fmt.Fprintf(stdout, "PR #%d (%s) in %s\n", it.PR, it.Branch, it.Lane)
		t := &Target{Worktree: it.Lane, Branch: it.Branch, MainRepo: mainRepo, RepoName: filepath.Base(mainRepo)}
		if err := MergeWait(t, method, deleteBranch, o, stdout, stderr); err != nil {
			var rest []string
			for _, r := range items[i+1:] {
				rest = append(rest, fmt.Sprintf("#%d", r.PR))
			}
			msg := fmt.Sprintf("queue stopped at PR #%d (%s): %v", it.PR, it.Branch, err)
			if len(rest) > 0 {
				msg += "\nnot attempted: " + strings.Join(rest, " ")
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("refused: %s", strings.Join(refused, " "))
	}
	return nil
}
