package workspace

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// When the PR's base branch has a merge queue, GitHub refuses a direct merge:
// the PR is enqueued, and the queue tests it on the branch as it is then and
// merges it. These tests drive the verb through the same gh seams the direct
// merge tests use, plus the queue's own.

const queueHead = "c0ffee0000000000000000000000000000000000"

// enqueueGH scripts the queue side of GitHub for one merge: what the PR's
// entry reads around the enqueue, and then, one per poll of a wait, the PR's
// state and its entry.
type enqueueGH struct {
	enqueued []string // "<pr>@<head>", in call order
	calls    []string // the lookups the verb made, in order
	pre      []*QueueEntry
	preUsed  int
	polls    []qPoll
	poll     int
	enqErr   error
	explain  string       // what the merge_group run's summary prints
	noRun    bool         // the queue removed the PR but no merge_group run exists
	removal  QueueRemoval // what the PR's timeline says whenever the PR is out of the queue
	dequeued []string
	repos    []string // the repository slug each queue call was given
}

type qPoll struct {
	state string
	entry *QueueEntry
}

func (q *enqueueGH) pollAt() qPoll {
	i := q.poll - 1
	if i < 0 {
		i = 0
	}
	if i >= len(q.polls) {
		i = len(q.polls) - 1
	}
	return q.polls[i]
}

// newQueueWorld is a merge world whose base branch has a merge queue.
func newQueueWorld(t *testing.T, ci CIStatus) (*ciWorld, *enqueueGH) {
	t.Helper()
	w := newCIWorld(t, tdd.CIAuto, ci)
	q := &enqueueGH{}
	oQ, oEnq, oEntry, oRun, oExplain := ghHasMergeQueue, ghEnqueuePR, ghQueueEntry, ghMergeGroupRun, explainMergeGroupRun
	oHead, oNow, oSleep, oQGate := ghPRHead, waitNow, waitSleep, premergeGateQueued
	oChecks, oLane := ghChecksAt, laneHeadSHA
	oRem, oDeq := ghQueueRemoval, ghDequeuePR
	t.Cleanup(func() {
		ghHasMergeQueue, ghEnqueuePR, ghQueueEntry, ghMergeGroupRun, explainMergeGroupRun = oQ, oEnq, oEntry, oRun, oExplain
		ghPRHead, waitNow, waitSleep, premergeGateQueued = oHead, oNow, oSleep, oQGate
		ghChecksAt, laneHeadSHA = oChecks, oLane
		ghQueueRemoval, ghDequeuePR = oRem, oDeq
	})
	ghHasMergeQueue = func(wt, repo, base string) (bool, error) { q.calls = append(q.calls, "detect "+base); return true, nil }
	ghEnqueuePR = func(wt, repo string, pr int, sha string) error {
		q.repos = append(q.repos, repo)
		q.enqueued = append(q.enqueued, fmt.Sprintf("%d@%s", pr, sha))
		return q.enqErr
	}
	ghQueueEntry = func(wt, repo string, pr int) (*QueueEntry, error) {
		if q.poll > 0 {
			return q.pollAt().entry, nil
		}
		if q.preUsed < len(q.pre) {
			e := q.pre[q.preUsed]
			q.preUsed++
			return e, nil
		}
		return nil, nil
	}
	// A wait reads the PR by branch while it waits for the head's checks, and by
	// number while it waits for the queue; only the second advances the script.
	ghChecksAt = func(string, string) ([]CheckRun, error) {
		return []CheckRun{run("go test", queueHead, "completed", "success")}, nil
	}
	laneHeadSHA = func(string) (string, error) { return queueHead, nil }
	ghPRHead = func(dir, ref string) (*PRHead, error) {
		if _, err := strconv.Atoi(ref); err != nil {
			return &PRHead{Number: 5, URL: "u", State: "OPEN", HeadRef: ref, HeadSHA: queueHead}, nil
		}
		q.poll++
		return &PRHead{Number: 5, URL: "u", State: q.pollAt().state, HeadRef: "feat/z", HeadSHA: queueHead}, nil
	}
	ghQueueRemoval = func(wt, repo string, pr int) (QueueRemoval, error) { return q.removal, nil }
	ghDequeuePR = func(wt, id string) error { q.dequeued = append(q.dequeued, id); return nil }
	ghMergeGroupRun = func(wt, repo string, pr int) (int64, error) {
		if q.noRun {
			return 0, nil
		}
		return 9001, nil
	}
	explainMergeGroupRun = func(wt string, id int64, w io.Writer) error {
		fmt.Fprint(w, q.explain)
		return nil
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	waitNow = func() time.Time { return now }
	waitSleep = func(d time.Duration) { now = now.Add(d) }
	// The queue's gate is the one under test; the plain gate, which refuses a
	// stale base, must never be the one that runs.
	premergeGateQueued = func(_ *Target, head string, _ *tdd.CIVerdict, _ io.Writer) error {
		w.gateRuns++
		w.gateHead = head
		return nil
	}
	premergeGate = func(*Target, string, *tdd.CIVerdict, io.Writer) error {
		t.Error("the plain pre-merge gate ran for a PR the merge queue lands")
		return nil
	}
	return w, q
}

func TestMergeQueue_NoQueueMergesDirectly(t *testing.T) {
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "green", SHA: "abc"})
	out, err := applyMerge(t, "")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if w.merges != 1 {
		t.Errorf("direct merges = %d, want 1 in a repo with no queue (stubMerge fails the test on any enqueue)", w.merges)
	}
	if !strings.Contains(out, "merged PR #5 (squash)") {
		t.Errorf("a repo with no queue must report the merge as before:\n%s", out)
	}
}

func TestMergeQueue_EnqueuesTheJudgedHeadInsteadOfMerging(t *testing.T) {
	w, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 2, Total: 3, State: "QUEUED"}}
	synced := 0
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { synced++; return nil })

	out, err := applyMerge(t, "")

	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if w.merges != 0 {
		t.Errorf("a direct merge was made (%d) on a branch whose queue GitHub says takes every PR", w.merges)
	}
	if want := "5@" + queueHead; len(q.enqueued) != 1 || q.enqueued[0] != want {
		t.Errorf("enqueued %v, want exactly [%s]: the enqueue must be bound to the judged head", q.enqueued, want)
	}
	if w.gateHead != queueHead {
		t.Errorf("the gate judged %q, want the PR head %s", w.gateHead, queueHead)
	}
	var queued []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(l, "queued PR #5") {
			queued = append(queued, l)
		}
	}
	if len(queued) != 1 || !strings.Contains(queued[0], "position 2 of 3") {
		t.Errorf("want one line `queued PR #5 … position 2 of 3`, got %q in:\n%s", queued, out)
	}
	if strings.Contains(out, "merged PR") {
		t.Errorf("a queued PR is not merged yet, but the output says it is:\n%s", out)
	}
	if synced != 0 {
		t.Errorf("local main was synced %d time(s) for a PR that has not merged", synced)
	}
}

func TestMergeQueue_SaysSoWhenGitHubReportsNoPositionYet(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, nil}

	out, err := applyMerge(t, "")

	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "queued PR #5") || !strings.Contains(out, "position not reported yet") {
		t.Errorf("an unknown position must be said, never invented:\n%s", out)
	}
}

func TestMergeQueue_APRAlreadyInTheQueueIsNotEnqueuedAgain(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{{Position: 1, Total: 1, State: "AWAITING_CHECKS"}}

	out, err := applyMerge(t, "")

	if err != nil {
		t.Fatal(err)
	}
	if len(q.enqueued) != 0 {
		t.Errorf("enqueued %v a PR that is already in the queue (a resumed run)", q.enqueued)
	}
	if !strings.Contains(out, "already") || !strings.Contains(out, "position 1 of 1") {
		t.Errorf("the output must say the PR is already queued, and where:\n%s", out)
	}
}

// A stale verdict is the queue's to settle: it tests the PR on the branch as
// it is. The gate that runs is the queue's, never the one that refuses.
func TestMergeQueue_AStaleBaseIsNotARefusal(t *testing.T) {
	w, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	noVerdictChecks(t)
	var handed bool
	premergeGateQueued = func(_ *Target, _ string, v *tdd.CIVerdict, _ io.Writer) error {
		w.gateRuns++
		handed = v != nil
		return nil
	}

	_, err := applyMerge(t, "")

	if err != nil {
		t.Fatalf("a PR whose checks are green on its head must be enqueued whatever base they judged: %v", err)
	}
	if w.gateRuns != 1 || !handed {
		t.Errorf("gate runs = %d, handed CI verdict = %v; want the queue's gate once, with GitHub's verdict", w.gateRuns, handed)
	}
	if len(q.enqueued) != 1 {
		t.Errorf("enqueued %v, want one", q.enqueued)
	}
}

// Everything that does not need a tree-equal CI verdict still refuses before
// the PR is enqueued.
func TestMergeQueue_TheLocalChecksStillRefuseBeforeEnqueueing(t *testing.T) {
	cases := []struct {
		name string
		ci   CIStatus
		arm  func()
		want string
	}{
		{name: "lane is not the PR head",
			ci: CIStatus{State: "green", SHA: "abc"},
			arm: func() {
				laneAtHead = func(string, string, int) error { return &JudgedHeadError{Msg: "lane HEAD aaaaaaa is not the PR head"} }
			},
			want: "lane HEAD aaaaaaa is not the PR head"},
		{name: "the PR's own checks are red",
			ci:   CIStatus{State: "red", Failing: 2, SHA: "abc"},
			want: "required checks are not green (red (2 failing))"},
		{name: "the PR's own checks are still running",
			ci:   CIStatus{State: "pending", SHA: "abc"},
			want: "required checks are not green (pending)"},
		{name: "an escape the PR must close is open",
			ci: CIStatus{State: "green", SHA: "abc"},
			arm: func() {
				escapeClosureBeforeMerge = func(string, int, io.Writer) error { return errors.New("escape not closed") }
			},
			want: "escape not closed"},
		{name: "the gate refuses the merged tree",
			ci: CIStatus{State: "green", SHA: "abc"},
			arm: func() {
				premergeGateQueued = func(*Target, string, *tdd.CIVerdict, io.Writer) error { return errors.New("gate: law refused") }
			},
			want: "refusing to merge feat/z: gate: law refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, q := newQueueWorld(t, tc.ci)
			if tc.arm != nil {
				tc.arm()
			}
			_, err := applyMerge(t, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
			if len(q.enqueued) != 0 {
				t.Errorf("enqueued %v despite the refusal", q.enqueued)
			}
		})
	}
}

func TestMergeQueue_AFailedEnqueueKeepsItsKindAndRecordsNothing(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.enqErr = &JudgedHeadError{Msg: "PR head moved after it was judged (merge bound to c0ffee0) — merge again to judge the new head"}

	_, err := applyMerge(t, "")

	var judged *JudgedHeadError
	if !errors.As(err, &judged) {
		t.Fatalf("error = %v, want the head-moved refusal (exit 2) to survive", err)
	}
	if got := ofKind(emitted(t), "merge"); len(got) != 0 {
		t.Errorf("recorded %d merge event(s) for a PR that was never queued", len(got))
	}
}

// A PR queued without --wait is a merge GitHub makes later; the verb records
// that it queued it so the trunk move that takes the merge in is not counted as
// one made outside the verb.
func TestMergeQueue_QueuedPRIsRecordedSoItsMergeIsNotCountedAsOutside(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})

	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}

	got := ofKind(emitted(t), "merge")
	if len(got) != 1 || got[0].Verdict != "queued" || got[0].Detail["pr"] != "5" {
		t.Fatalf("merge events = %+v, want one with verdict queued for pr 5", got)
	}
}

func TestMergeQueue_TheBaseBranchTheQueueIsAskedAboutIsThePRs(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	ghViewPR = func(wt, branch string) (*PRInfo, error) {
		return &PRInfo{Number: 5, URL: "u", HeadSHA: queueHead, BaseRef: "release/2"}, nil
	}

	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}

	if len(q.calls) == 0 || q.calls[0] != "detect release/2" {
		t.Errorf("calls = %v, want the queue looked up for the PR's own base release/2 first", q.calls)
	}
}

func TestMergeQueue_AnUnreadableQueueStateRefusesInsteadOfGuessing(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	ghHasMergeQueue = func(string, string, string) (bool, error) { return false, errors.New("HTTP 502") }

	_, err := applyMerge(t, "")

	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "merge queue") {
		t.Fatalf("error = %v, want the refusal to name the merge queue read and its cause", err)
	}
	if len(q.enqueued) != 0 {
		t.Errorf("enqueued %v on a guess", q.enqueued)
	}
}
