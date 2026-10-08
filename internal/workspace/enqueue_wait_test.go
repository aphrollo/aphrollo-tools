package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `merge --wait` on a queued PR waits for the PR to be MERGED, not for its
// checks to pass, and reports why when the queue drops it.

func queueTarget() *Target {
	return &Target{Worktree: "/x", Branch: "feat/z", MainRepo: "/x/main", RepoName: "r"}
}

var queueWait = WaitOpts{Interval: 30 * time.Second, Timeout: 10 * time.Minute}

func TestMergeWait_QueueWaitsForMergedAndPrintsEachChangeOnce(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	w, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 2, Total: 2, State: "QUEUED"}}
	q.polls = []qPoll{
		{"OPEN", &QueueEntry{Position: 2, Total: 2, State: "QUEUED"}},
		{"OPEN", &QueueEntry{Position: 2, Total: 2, State: "QUEUED"}},
		{"OPEN", &QueueEntry{Position: 1, Total: 1, State: "AWAITING_CHECKS"}},
		{"MERGED", nil},
	}
	synced, deleted := 0, 0
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { synced++; return nil })
	ghDeleteRemoteBranch = func(string, string) (bool, error) { deleted++; return false, nil }

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err != nil {
		t.Fatalf("MergeWait: %v\n%s", err, out.String())
	}
	s := out.String()
	if n := strings.Count(s, "position 2 of 2"); n != 2 { // the enqueue line, then one wait line
		t.Errorf("`position 2 of 2` appears %d times, want 2 (queued line + one wait line, not one per poll):\n%s", n, s)
	}
	if !strings.Contains(s, "position 1 of 1") {
		t.Errorf("the move to position 1 is not reported:\n%s", s)
	}
	if !strings.Contains(s, "merged PR #5 (merge queue)") {
		t.Errorf("the merge is not reported:\n%s", s)
	}
	if w.merges != 0 {
		t.Errorf("direct merges = %d, want 0", w.merges)
	}
	if synced != 1 || deleted != 1 {
		t.Errorf("post-merge steps: synced %d, branch deletes %d; want 1 and 1", synced, deleted)
	}
	got := ofKind(emitted(t), "merge")
	if len(got) != 1 || got[0].Verdict != "ok" || got[0].Detail["pr"] != "5" {
		t.Errorf("merge events = %+v, want one ok event for pr 5 (recorded before the sync)", got)
	}
	if len(got) == 1 {
		if _, err := time.Parse(time.RFC3339, got[0].Detail["enqueued_at"]); err != nil {
			t.Errorf("merge detail = %v, want enqueued_at: when the PR entered the queue, so the time the queue took is known", got[0].Detail)
		}
	}
}

func TestMergeWait_QueueRemovalNamesTheFailingJobAndFails(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "AWAITING_CHECKS"}}
	q.polls = []qPoll{
		{"OPEN", &QueueEntry{Position: 1, Total: 1, State: "AWAITING_CHECKS"}},
		{"OPEN", nil},
		{"OPEN", nil}, // the re-read that confirms it is not merged
	}
	q.removal = QueueRemoval{Removed: true, Reason: "failed_checks"}
	q.explain = "run 9001 Pipeline attempt 1: failure (merge_group gh-readonly-queue/main/pr-5-c0ffee0 e566f2f)\nFAIL test: TestSomething\n"
	synced := 0
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { synced++; return nil })

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err == nil {
		t.Fatal("a PR the queue removed was reported as merged")
	}
	for _, want := range []string{"removed from the merge queue", "FAIL test: TestSomething", "run 9001"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	var judged *JudgedHeadError
	if errors.As(err, &judged) {
		t.Errorf("a queue removal is exit 1, not the head refusals' 2")
	}
	if synced != 0 {
		t.Errorf("synced %d time(s) after a removal", synced)
	}
}

func TestMergeWait_QueueRemovalWithNoMergeGroupRunSaysSo(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "QUEUED"}}
	q.polls = []qPoll{{"OPEN", &QueueEntry{Position: 1, Total: 1, State: "QUEUED"}}, {"OPEN", nil}, {"OPEN", nil}}
	q.removal = QueueRemoval{Removed: true, Reason: "failed_checks"}
	q.noRun = true

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err == nil || !strings.Contains(err.Error(), "removed from the merge queue") || !strings.Contains(err.Error(), "no merge_group run") {
		t.Fatalf("error = %v, want the removal with the absence of a merge_group run named", err)
	}
}

// The PR can merge between the poll's two reads: state first (still open),
// then the entry (gone because it merged). That is a merge, not a removal.
func TestMergeWait_AnEntryThatMergedBetweenTheReadsIsNotARemoval(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "MERGEABLE"}}
	q.polls = []qPoll{
		{"OPEN", &QueueEntry{Position: 1, Total: 1, State: "MERGEABLE"}},
		{"OPEN", nil},
		{"MERGED", nil},
	}
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })

	var out, errb bytes.Buffer
	if err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb); err != nil {
		t.Fatalf("a PR that merged was reported as removed: %v", err)
	}
}

func TestMergeWait_APRClosedWhileQueuedFails(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "QUEUED"}}
	q.polls = []qPoll{{"CLOSED", nil}}

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("error = %v, want one saying the PR was closed without merging", err)
	}
}

func TestMergeWait_QueueTimesOutWithoutClaimingAMerge(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 3, Total: 3, State: "QUEUED"}}
	q.polls = []qPoll{{"OPEN", &QueueEntry{Position: 3, Total: 3, State: "QUEUED"}}}

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, WaitOpts{Interval: time.Minute, Timeout: 5 * time.Minute}, &out, &errb)

	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "merge queue") {
		t.Fatalf("error = %v, want a timeout naming the merge queue", err)
	}
	if strings.Contains(out.String(), "merged PR") {
		t.Errorf("claimed a merge after a timeout:\n%s", out.String())
	}
}

// The multi-PR form enqueues every PR before it waits for any: the queue
// orders them and builds each on the ones ahead, so waiting for the first to
// land before the second is enqueued would serialize what the queue pipelines.
func TestRunMergeQueue_QueueEnqueuesEveryPRBeforeWaitingForAny(t *testing.T) {
	green := []CheckRun{run("go test", newSHA, "completed", "success")}
	a := &fakePR{number: 11, branch: "lane/a", steps: []ciStep{{head: newSHA, checks: green}}}
	b := &fakePR{number: 12, branch: "lane/b", steps: []ciStep{{head: newSHA, checks: green}}}
	f := &fakeCI{prs: []*fakePR{a, b}, laneHead: map[string]string{"/w/a": newSHA, "/w/b": newSHA}}
	install(t, f)
	noVerdictChecks(t)

	var trace []string
	oQ, oEnq, oEntry, oHead, oGateQ := ghHasMergeQueue, ghEnqueuePR, ghQueueEntry, ghPRHead, premergeGateQueued
	t.Cleanup(func() {
		ghHasMergeQueue, ghEnqueuePR, ghQueueEntry, ghPRHead, premergeGateQueued = oQ, oEnq, oEntry, oHead, oGateQ
	})
	ghHasMergeQueue = func(string, string, string) (bool, error) { return true, nil }
	premergeGateQueued = func(*Target, string, *tdd.CIVerdict, io.Writer) error { return nil }
	oText := ghPRText
	t.Cleanup(func() { ghPRText = oText })
	ghPRText = func(string, string) (string, string, error) { return "Merge each lane through the queue", "", nil }
	ghEnqueuePR = func(_, _ string, pr int, _ string) error {
		trace = append(trace, fmt.Sprintf("enqueue %d", pr))
		return nil
	}
	ghQueueEntry = func(string, string, int) (*QueueEntry, error) { return nil, nil }
	ghPRHead = func(dir, ref string) (*PRHead, error) {
		if n, err := strconv.Atoi(ref); err == nil {
			trace = append(trace, fmt.Sprintf("wait %d", n))
			return &PRHead{Number: n, State: "MERGED", HeadSHA: newSHA}, nil
		}
		return oHead(dir, ref)
	}
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })

	items := []QueueItem{
		{PR: 11, Branch: "lane/a", HeadSHA: newSHA, Lane: "/w/a"},
		{PR: 12, Branch: "lane/b", HeadSHA: newSHA, Lane: "/w/b"},
	}
	var out, errb bytes.Buffer
	if err := RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("RunMergeQueue: %v\n%s", err, out.String())
	}

	want := "enqueue 11,enqueue 12,wait 11,wait 12"
	if got := strings.Join(trace, ","); got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}
