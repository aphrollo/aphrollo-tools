package workspace

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The coordinator used to wait for CI in a hand-rolled shell loop and then run
// `workspace merge`. It misfired three ways: it read checks that belonged to an
// older head, it merged on the empty set GitHub shows right after a push, and
// its awk split check names on spaces. `merge --wait` owns the wait, so these
// tests drive it through a scripted gh and a fake clock — never a real sleep.

// ciStep is one poll's view of a PR: the head GitHub reports and the checks on
// it. The last step repeats once the script runs out.
type ciStep struct {
	head   string
	checks []CheckRun
}

// fakePR is one PR's scripted history.
type fakePR struct {
	number int
	branch string
	steps  []ciStep
	cursor int
}

func (p *fakePR) at(i int) ciStep {
	if i >= len(p.steps) {
		i = len(p.steps) - 1
	}
	return p.steps[i]
}

// fakeCI stands in for every gh and git read the wait and the merge make.
// Each wait poll reads the PR head exactly once, which advances that PR's
// script; the checks read answers from the step that head read selected.
type fakeCI struct {
	now      time.Time
	slept    time.Duration
	prs      []*fakePR
	cur      *fakePR
	lanes    []worktreeEntry
	laneHead map[string]string // worktree -> the lane's own HEAD
	merged   []string
	refuse   map[string]error // branch -> ghMergePR's refusal
}

func (f *fakeCI) byBranch(b string) *fakePR {
	for _, p := range f.prs {
		if p.branch == b {
			return p
		}
	}
	return nil
}

func install(t *testing.T, f *fakeCI) {
	t.Helper()
	if f.now.IsZero() {
		f.now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	}
	oHead, oChecks, oLane, oLanes := ghPRHead, ghChecksAt, laneHeadSHA, listLanes
	oNow, oSleep := waitNow, waitSleep
	t.Cleanup(func() {
		ghPRHead, ghChecksAt, laneHeadSHA, listLanes = oHead, oChecks, oLane, oLanes
		waitNow, waitSleep = oNow, oSleep
	})
	ghPRHead = func(dir, ref string) (*PRHead, error) {
		if n, err := strconv.Atoi(ref); err == nil {
			for _, p := range f.prs {
				if p.number == n {
					return &PRHead{Number: n, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", n), State: "OPEN", HeadRef: p.branch, HeadSHA: p.at(0).head}, nil
				}
			}
			return nil, fmt.Errorf("gh pr view %d: no pull requests found", n)
		}
		p := f.byBranch(ref)
		if p == nil {
			return nil, fmt.Errorf("no PR for %s", ref)
		}
		s := p.at(p.cursor)
		p.cursor++
		f.cur = p
		return &PRHead{Number: p.number, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", p.number), State: "OPEN", HeadRef: p.branch, HeadSHA: s.head}, nil
	}
	ghChecksAt = func(dir, sha string) ([]CheckRun, error) {
		return f.cur.at(f.cur.cursor - 1).checks, nil
	}
	laneHeadSHA = func(wt string) (string, error) { return f.laneHead[wt], nil }
	listLanes = func(repo string) ([]worktreeEntry, error) { return f.lanes, nil }
	waitNow = func() time.Time { return f.now }
	waitSleep = func(d time.Duration) { f.now = f.now.Add(d); f.slept += d }

	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			p := f.byBranch(branch)
			return &PRInfo{Number: p.number, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", p.number), State: "OPEN"}, nil
		},
		func(wt, branch, method string) error {
			if err := f.refuse[branch]; err != nil {
				return err
			}
			f.merged = append(f.merged, branch)
			return nil
		},
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error { return nil })
	stubPremergeGate(t, func(tgt *Target, log io.Writer) error { return nil })
}

func run(name, sha, status, conclusion string) CheckRun {
	return CheckRun{Name: name, SHA: sha, Status: status, Conclusion: conclusion, URL: "https://github.com/o/r/actions/runs/1/" + strings.ReplaceAll(name, " ", "-")}
}

var testWait = WaitOpts{Interval: 30 * time.Second, Timeout: 10 * time.Minute}

const (
	oldSHA = "aaaaaaa1111111111111111111111111111111111"
	newSHA = "bbbbbbb2222222222222222222222222222222222"
)

// Right after a push GitHub still reports the old head, whose checks are all
// green. That green belongs to a commit the lane has moved past: merging on it
// is the first misfire.
func TestMergeWait_GreenChecksOnAnOlderHeadDoNotMerge(t *testing.T) {
	pr := &fakePR{number: 11, branch: "lane/a", steps: []ciStep{
		{head: oldSHA, checks: []CheckRun{run("go test", oldSHA, "completed", "success")}},
		{head: newSHA, checks: []CheckRun{run("go test", oldSHA, "completed", "success")}},
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "in_progress", "")}},
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "success")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/a": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/a", Branch: "lane/a", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)
	if err != nil {
		t.Fatalf("MergeWait: %v\n%s", err, out.String())
	}
	if pr.cursor != 4 {
		t.Errorf("merged after %d polls, want 4: the green on the old head and the old-SHA checks under the new head must both count as not started", pr.cursor)
	}
	if len(f.merged) != 1 {
		t.Errorf("merged %v, want exactly lane/a once", f.merged)
	}
}

// The empty set GitHub shows right after a push is not "nothing to wait for":
// it is pending until the head's first check appears — the second misfire.
func TestMergeWait_EmptySetAfterPushIsPendingUntilTheFirstCheckAppears(t *testing.T) {
	pr := &fakePR{number: 12, branch: "lane/b", steps: []ciStep{
		{head: newSHA},
		{head: newSHA},
		{head: newSHA},
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "queued", "")}},
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "in_progress", "")}},
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "success")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/b": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	if err := MergeWait(&Target{Worktree: "/w/b", Branch: "lane/b", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("MergeWait: %v\n%s", err, out.String())
	}
	if pr.cursor != 6 {
		t.Errorf("merged after %d polls, want 6", pr.cursor)
	}
	if f.slept != 5*testWait.Interval {
		t.Errorf("slept %v, want 5 intervals of %v", f.slept, testWait.Interval)
	}
	// Six polls, three states (no check yet, running, green): one line each.
	waitLines := 0
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "[wait]") {
			waitLines++
		}
	}
	if waitLines != 3 {
		t.Errorf("printed %d wait lines, want 3 (one per state change, not one per poll):\n%s", waitLines, out.String())
	}
}

// A head whose first check never appears is bounded by the timeout, and a
// timeout is never a merge.
func TestMergeWait_NoCheckEverAppearingTimesOutWithoutMerging(t *testing.T) {
	pr := &fakePR{number: 13, branch: "lane/c", steps: []ciStep{{head: newSHA}}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/c": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/c", Branch: "lane/c", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Fatal("a head with no check must time out, not merge")
	}
	if len(f.merged) != 0 {
		t.Errorf("merged %v on a timeout", f.merged)
	}
	if f.slept > testWait.Timeout {
		t.Errorf("slept %v, past the %v timeout", f.slept, testWait.Timeout)
	}
}

// A failed check stops the wait without merging and names every failing check
// with its URL, whole — a name with spaces is one name (the third misfire).
func TestMergeWait_FailedCheckStopsAndNamesEachFailureWithItsURL(t *testing.T) {
	pr := &fakePR{number: 14, branch: "lane/d", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{
			run("go test", newSHA, "completed", "success"),
			run("lint / go vet", newSHA, "completed", "failure"),
			run("mutants at merge", newSHA, "completed", "timed_out"),
			run("docs", newSHA, "in_progress", ""),
		}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/d": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/d", Branch: "lane/d", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Fatal("a failed check must stop the wait")
	}
	if len(f.merged) != 0 {
		t.Errorf("merged %v with a failed check", f.merged)
	}
	msg := err.Error()
	for _, want := range []string{
		"lint / go vet  https://github.com/o/r/actions/runs/1/lint-/-go-vet",
		"mutants at merge  https://github.com/o/r/actions/runs/1/mutants-at-merge",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("failure message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "go test  https") {
		t.Errorf("a passing check is listed as failing:\n%s", msg)
	}
}

func queueFake() *fakeCI {
	green := func(b string) []ciStep {
		return []ciStep{
			{head: newSHA, checks: []CheckRun{run("go test", newSHA, "in_progress", "")}},
			{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "success")}},
		}
	}
	return &fakeCI{
		prs: []*fakePR{
			{number: 21, branch: "lane/one", steps: green("lane/one")},
			{number: 22, branch: "lane/two", steps: green("lane/two")},
			{number: 23, branch: "lane/three", steps: green("lane/three")},
		},
		lanes: []worktreeEntry{
			{Path: "/w/one", Branch: "lane/one"},
			{Path: "/w/two", Branch: "lane/two"},
			{Path: "/w/three", Branch: "lane/three"},
		},
		laneHead: map[string]string{"/w/one": newSHA, "/w/two": newSHA, "/w/three": newSHA},
	}
}

func TestMergeQueue_MergesEachPRInOrder(t *testing.T) {
	f := queueFake()
	install(t, f)

	items, err := PlanMergeQueue("/r", []int{22, 21})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("RunMergeQueue: %v\n%s", err, out.String())
	}
	if got := strings.Join(f.merged, ","); got != "lane/two,lane/one" {
		t.Errorf("merged %q, want lane/two,lane/one — the order given", got)
	}
}

// A PR whose lane worktree is gone is refused by name and the queue moves on:
// the missing lane is a fact about that PR, not about the ones after it.
func TestMergeQueue_MissingLaneIsRefusedByNameAndTheQueueContinues(t *testing.T) {
	f := queueFake()
	f.lanes = f.lanes[1:] // lane/one has no worktree
	install(t, f)

	items, err := PlanMergeQueue("/r", []int{21, 22})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Error("a refused PR must make the queue's run fail, even when the rest merged")
	}
	if got := strings.Join(f.merged, ","); got != "lane/two" {
		t.Errorf("merged %q, want lane/two only", got)
	}
	if !strings.Contains(out.String()+errb.String(), "#21") {
		t.Errorf("the refusal does not name PR #21:\n%s%s", out.String(), errb.String())
	}
}

// A refused merge stops the queue: the PRs after it are never attempted, and
// the report says which PR stopped it and which were left.
func TestMergeQueue_RefusedMergeStopsTheQueueAndListsTheRest(t *testing.T) {
	f := queueFake()
	f.refuse = map[string]error{"lane/two": fmt.Errorf("gh pr merge: Pull request is not mergeable: the merge commit cannot be cleanly created")}
	install(t, f)

	items, err := PlanMergeQueue("/r", []int{21, 22, 23})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Fatal("a refused merge must fail the queue")
	}
	if got := strings.Join(f.merged, ","); got != "lane/one" {
		t.Errorf("merged %q, want lane/one only — nothing past the refusal", got)
	}
	if third := f.byBranch("lane/three"); third.cursor != 0 {
		t.Errorf("PR #23 was polled %d times after the queue stopped", third.cursor)
	}
	report := err.Error()
	if !strings.Contains(report, "#22") || !strings.Contains(report, "cannot be cleanly created") {
		t.Errorf("the stop does not name PR #22 and its reason:\n%s", report)
	}
	if !strings.Contains(report, "not attempted: #23") {
		t.Errorf("the stop does not list #23 as not attempted:\n%s", report)
	}
}

// A failed check is a stop, not a skip: the queue never jumps past it.
func TestMergeQueue_FailedCheckStopsTheQueue(t *testing.T) {
	f := queueFake()
	f.prs[0].steps = []ciStep{{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "failure")}}}
	install(t, f)

	items, _ := PlanMergeQueue("/r", []int{21, 22})
	var out, errb bytes.Buffer
	err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb)
	if err == nil {
		t.Fatal("a failed check must fail the queue")
	}
	if len(f.merged) != 0 {
		t.Errorf("merged %v past a failed check", f.merged)
	}
	if !strings.Contains(err.Error(), "not attempted: #22") {
		t.Errorf("the stop does not list #22 as not attempted:\n%s", err)
	}
}

func TestMergeQueue_PlanShowsEachPRsLaneAndHeadSHA(t *testing.T) {
	f := queueFake()
	f.lanes = f.lanes[1:]
	install(t, f)

	items, err := PlanMergeQueue("/r", []int{21, 22})
	if err != nil {
		t.Fatal(err)
	}
	plan := RenderMergeQueue(items)
	for _, want := range []string{"#21", "lane/one", "(no lane worktree)", "#22", "/w/two", newSHA[:7]} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan lacks %q:\n%s", want, plan)
		}
	}
	if len(f.merged) != 0 || f.slept != 0 {
		t.Error("planning must neither wait nor merge")
	}
}

func TestPRNumbers_OnlyAnAllNumericListIsAQueue(t *testing.T) {
	if got, ok := PRNumbers([]string{"12", "7"}); !ok || len(got) != 2 || got[0] != 12 || got[1] != 7 {
		t.Errorf("PRNumbers(12 7) = %v, %v", got, ok)
	}
	for _, pos := range [][]string{nil, {"aphrollo-tools", "lane/x"}, {"12", "lane/x"}, {"0"}, {"-3"}} {
		if _, ok := PRNumbers(pos); ok {
			t.Errorf("PRNumbers(%q) read as a queue", pos)
		}
	}
}

// In a lane, --dry --wait prints the same plan for the lane's own PR: its
// number, branch, head SHA and the lane itself — and reads nothing twice.
func TestPlanLane_NamesTheLanePRAndItsHead(t *testing.T) {
	f := queueFake()
	install(t, f)

	items, err := PlanLane(&Target{Worktree: "/w/two", Branch: "lane/two", MainRepo: "/r", RepoName: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PR != 22 || items[0].Lane != "/w/two" || items[0].HeadSHA != newSHA || items[0].Problem != "" {
		t.Errorf("PlanLane = %+v, want PR #22 in /w/two at %s", items, short(newSHA))
	}
}
