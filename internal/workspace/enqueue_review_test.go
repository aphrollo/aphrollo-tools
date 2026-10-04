package workspace

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Behind a merge queue the squash message is written by GitHub from the PR's
// title and body, so what the verb scrubs on the direct path (a tell footer, a
// closing trailer only a lane commit carries) has to be done to the PR itself.

// queuedUndercover is an undercover repo whose PR is behind a merge queue, with
// the PR body held so a rewrite is visible to the read-back.
func queuedUndercover(t *testing.T, title, body string, lane ...string) (*Merge, *string, *[]string, *[]string) {
	t.Helper()
	repo, commit := mergeUndercoverRepo(t, true)
	for _, msg := range lane {
		commit(nil, "-m", msg)
	}
	stubMergeUndercover(t, title, body)
	held := body
	var edits, enqueued []string
	oText, oEdit, oQ, oEnq, oEntry, oGate := ghPRText, ghEditPRBody, ghHasMergeQueue, ghEnqueuePR, ghQueueEntry, premergeGateQueued
	t.Cleanup(func() {
		ghPRText, ghEditPRBody, ghHasMergeQueue, ghEnqueuePR, ghQueueEntry, premergeGateQueued = oText, oEdit, oQ, oEnq, oEntry, oGate
	})
	ghPRText = func(string, string) (string, string, error) { return title, held, nil }
	ghEditPRBody = func(_, _, b string) error { edits = append(edits, b); held = b; return nil }
	ghHasMergeQueue = func(string, string, string) (bool, error) { return true, nil }
	ghEnqueuePR = func(_, _ string, pr int, _ string) error { enqueued = append(enqueued, title); return nil }
	ghQueueEntry = func(string, string, int) (*QueueEntry, error) { return nil, nil }
	premergeGateQueued = func(*Target, string, *tdd.CIVerdict, io.Writer) error { return nil }
	m, err := MergePlan(targetFor(repo, "lane/x"), "squash", false)
	if err != nil {
		t.Fatal(err)
	}
	return m, &held, &edits, &enqueued
}

func TestMergeQueue_AFooterAndAMissingClosesAreFixedOnThePRBeforeItIsEnqueued(t *testing.T) {
	m, held, edits, enqueued := queuedUndercover(t, "Fix the timer",
		"Fixes the retry timer.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)", "Closes #77")

	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}

	if len(*edits) != 1 || *held != "Fixes the retry timer.\n\nCloses #77" {
		t.Errorf("PR body edits = %q, body now %q; want one edit to the scrubbed body with the lane's Closes", *edits, *held)
	}
	if strings.Contains(*held, "Generated with") {
		t.Error("the tell footer is still on the PR the queue will squash")
	}
	if len(*enqueued) != 1 {
		t.Errorf("enqueued %d time(s), want 1 after the rewrite", len(*enqueued))
	}
}

func TestMergeQueue_ACleanBodyIsLeftAlone(t *testing.T) {
	m, _, edits, enqueued := queuedUndercover(t, "Fix the timer", "Fixes the retry timer.")

	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if len(*edits) != 0 || len(*enqueued) != 1 {
		t.Errorf("edits = %q, enqueues = %d; want none and one", *edits, len(*enqueued))
	}
}

func TestMergeQueue_ARewriteThatDidNotTakeRefusesWithoutEnqueueing(t *testing.T) {
	m, _, _, enqueued := queuedUndercover(t, "Fix the timer", "Fixes it.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)")
	ghEditPRBody = func(string, string, string) error { return nil } // accepted, never applied

	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)

	if err == nil || !strings.Contains(err.Error(), "still holds the old one") {
		t.Fatalf("error = %v, want a refusal saying the body did not change", err)
	}
	if len(*enqueued) != 0 {
		t.Error("enqueued with the footer still on the PR")
	}
}

func TestMergeQueue_AMovedHeadAfterTheEnqueueIsTakenOutAndRefused(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {ID: "ENTRY1", Position: 1, Total: 1, State: "QUEUED"}}
	reads := 0
	ghViewPR = func(string, string) (*PRInfo, error) {
		reads++
		head := queueHead
		if reads > 1 { // the read after the enqueue
			head = "deadbeef00000000000000000000000000000000"
		}
		return &PRInfo{Number: 5, URL: "u", HeadSHA: head}, nil
	}

	_, err := applyMerge(t, "")

	var judged *JudgedHeadError
	if !errors.As(err, &judged) {
		t.Fatalf("error = %v, want the head refusal (exit 2)", err)
	}
	if len(q.dequeued) != 1 || q.dequeued[0] != "ENTRY1" {
		t.Errorf("dequeued %v, want the entry that was just made", q.dequeued)
	}
}

func TestMergeQueue_ThePRsBaseRepositoryIsWhereItIsEnqueued(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	ghViewPR = func(string, string) (*PRInfo, error) {
		return &PRInfo{Number: 5, URL: "u", HeadSHA: queueHead, BaseRepo: "upstream/widgets"}, nil
	}

	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}
	if len(q.repos) != 1 || q.repos[0] != "upstream/widgets" {
		t.Errorf("enqueue was given repos %v, want [upstream/widgets]", q.repos)
	}
}

func TestMergeQueue_ANamedMethodIsSaidToBeIgnored(t *testing.T) {
	newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	m, err := MergePlan(targetFor("/x", "feat/z"), "rebase", true)
	if err != nil {
		t.Fatal(err)
	}
	m.MethodSet = true

	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--rebase is ignored") {
		t.Errorf("the ignored flag is not named:\n%s", out.String())
	}

	m.MethodSet = false
	out.Reset()
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "is ignored") {
		t.Errorf("a method nobody named was reported as ignored:\n%s", out.String())
	}
}

// A PR dropped before the first poll saw it in the queue is reported with the
// queue's reason, promptly, instead of waiting out the timeout.
func TestMergeWait_APRDroppedBeforeAnyPollSawItIsReportedByTheTimeline(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, nil}
	q.polls = []qPoll{{"OPEN", nil}}
	q.removal = QueueRemoval{Removed: true, Reason: "failed_checks"}
	q.explain = "run 9001 Pipeline attempt 1: failure\nFAIL test: TestSomething\n"

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err == nil || !strings.Contains(err.Error(), "failed_checks") || !strings.Contains(err.Error(), "FAIL test: TestSomething") {
		t.Fatalf("error = %v, want the removal with GitHub's reason and the failing job", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Error("waited out the timeout instead of reading the removal")
	}
}

func TestMergeWait_AutoMergeWaitingForChecksIsNotARemoval(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, nil}
	q.polls = []qPoll{{"OPEN", nil}, {"OPEN", nil}, {"OPEN", nil}, {"OPEN", nil}, {"OPEN", nil}, {"MERGED", nil}}
	q.removal = QueueRemoval{AutoMerge: true}
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })

	var out, errb bytes.Buffer
	if err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb); err != nil {
		t.Fatalf("auto-merge pending was read as a removal: %v", err)
	}
	if !strings.Contains(out.String(), "auto-merge is on") {
		t.Errorf("the wait must say auto-merge is on and waiting for checks:\n%s", out.String())
	}
}

// One poll out of the queue is a grace period, not a removal: the entry comes
// back, or the merge shows a moment later.
func TestMergeWait_AShortGapOutOfTheQueueIsNotARemoval(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	back := &QueueEntry{Position: 1, Total: 1, State: "AWAITING_CHECKS"}
	q.pre = []*QueueEntry{nil, back}
	q.polls = []qPoll{{"OPEN", back}, {"OPEN", nil}, {"OPEN", nil}, {"OPEN", back}, {"OPEN", nil}, {"OPEN", nil}, {"MERGED", nil}}
	q.removal = QueueRemoval{Removed: true, Reason: "failed_checks"}
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })

	var out, errb bytes.Buffer
	if err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb); err != nil {
		t.Fatalf("a gap shorter than the grace period was read as a removal: %v", err)
	}
}

func movedHeadWorld(t *testing.T, viewErrAfter error) *enqueueGH {
	t.Helper()
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	reads := 0
	ghViewPR = func(string, string) (*PRInfo, error) {
		reads++
		if reads > 1 {
			if viewErrAfter != nil {
				return nil, viewErrAfter
			}
			return &PRInfo{Number: 5, URL: "u", HeadSHA: "deadbeef00000000000000000000000000000000"}, nil
		}
		return &PRInfo{Number: 5, URL: "u", HeadSHA: queueHead}, nil
	}
	return q
}

// Only a dequeue that happened may be reported as one.
func TestMergeQueue_AMovedHeadWithAnUnreadableEntryIsNotReportedAsDequeued(t *testing.T) {
	q := movedHeadWorld(t, nil)
	q.pre = []*QueueEntry{nil, nil} // the entry cannot be found after the enqueue

	_, err := applyMerge(t, "")

	var judged *JudgedHeadError
	if !errors.As(err, &judged) || strings.Contains(err.Error(), "was taken out") ||
		!strings.Contains(err.Error(), "STILL QUEUED") || !strings.Contains(err.Error(), "gh pr merge --disable-auto 5") {
		t.Fatalf("error = %v, want a head refusal saying it is still queued, with the command to dequeue", err)
	}
	if len(q.dequeued) != 0 {
		t.Errorf("dequeued %v with no entry to dequeue", q.dequeued)
	}
}

func TestMergeQueue_AFailedDequeueIsNotReportedAsDequeued(t *testing.T) {
	q := movedHeadWorld(t, nil)
	q.pre = []*QueueEntry{nil, {ID: "E1", Position: 1, Total: 1}}
	ghDequeuePR = func(string, string) error { return errors.New("HTTP 502") }

	_, err := applyMerge(t, "")

	if err == nil || strings.Contains(err.Error(), "was taken out") || !strings.Contains(err.Error(), "STILL QUEUED") {
		t.Fatalf("error = %v, want one saying the PR is still queued", err)
	}
}

func TestMergeQueue_AnUnreadableHeadAfterTheEnqueueIsReported(t *testing.T) {
	movedHeadWorld(t, errors.New("HTTP 502"))

	_, err := applyMerge(t, "")

	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "unverified head") {
		t.Fatalf("error = %v, want the read failure and the unverified head named", err)
	}
}

// The queue's own run of the PR failed: the removal is a red the measures
// count, written as a ci event of the queue's.
func TestMergeWait_AQueueRemovalForFailedChecksRecordsARedCIEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, nil}
	q.polls = []qPoll{{"OPEN", nil}}
	q.removal = QueueRemoval{Removed: true, Reason: "failed_checks"}

	var out, errb bytes.Buffer
	if err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb); err == nil {
		t.Fatal("a removed PR must fail the wait")
	}

	var queueReds []tdd.Event
	for _, e := range ofKind(emitted(t), "ci") {
		if e.Detail["ci"] == "queue" {
			queueReds = append(queueReds, e)
		}
	}
	if len(queueReds) != 1 || queueReds[0].Verdict != "red" || queueReds[0].Detail["cause"] != "queue" || queueReds[0].Detail["pr"] != "5" {
		t.Fatalf("queue ci events = %+v, want one red of cause queue for PR 5", queueReds)
	}
}

// A removal for another reason (a push to the PR, a dequeue) is not a red run.
func TestMergeWait_AQueueRemovalForAnotherReasonRecordsNoCIEvent(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, nil}
	q.polls = []qPoll{{"OPEN", nil}}
	q.removal = QueueRemoval{Removed: true, Reason: "auto-merge disabled"}

	_ = MergeWait(queueTarget(), "squash", true, queueWait, &bytes.Buffer{}, &bytes.Buffer{})

	for _, e := range ofKind(emitted(t), "ci") {
		if e.Detail["ci"] == "queue" {
			t.Fatalf("recorded %+v for a removal that was not a failed run", e)
		}
	}
}
