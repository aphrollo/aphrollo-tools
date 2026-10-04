package workspace

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A PR the verb queued and nobody waited for is merged by GitHub later. Local
// trunk taking the merge in is the first the log can see of it: it is recorded
// as the merge the verb queued (the lane's, no escape), not as an outside one,
// so the lane's speed ends at the merge.
func TestSync_ARecordedQueuedPRThatTrunkTookInIsMergedOnItsLaneNotOutside(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	clone := repoWithOrigin(t)
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Lane: "lane/q", Verdict: "queued",
		Detail: map[string]string{"pr": "1200", "method": "merge queue"}})
	sha := landOnOrigin(t, clone, "Queue the thing (#1200)")

	var first bytes.Buffer
	for i := range 2 { // a second pass records nothing more
		out := &bytes.Buffer{}
		if i == 0 {
			out = &first
		}
		if err := Sync(clone, false, out, &bytes.Buffer{}); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}
	if !strings.Contains(first.String(), "landed by the merge queue after `workspace merge` queued them: #1200") || strings.Contains(first.String(), "made outside") {
		t.Errorf("sync output = %q, want the PR named as queue-landed and not as made outside", first.String())
	}

	if outside := outsideMerges(t); len(outside) != 0 {
		t.Fatalf("outside merge events = %+v, want none for a queued PR", outside)
	}
	if escapes := ofKind(emitted(t), "escape"); len(escapes) != 0 {
		t.Fatalf("escape events = %+v, want none for a queued PR", escapes)
	}
	var oks []tdd.Event
	for _, e := range ofKind(emitted(t), "merge") {
		if e.Verdict == "ok" {
			oks = append(oks, e)
		}
	}
	if len(oks) != 1 || oks[0].Lane != "lane/q" || oks[0].Detail["pr"] != "1200" || oks[0].Detail["sha"] != sha || oks[0].Detail["method"] != "merge queue" {
		t.Fatalf("merge ok events = %+v, want one for lane/q PR 1200 at %s by merge queue", oks, sha)
	}
}

func queueDropWorld(t *testing.T, rem QueueRemoval) *int {
	t.Helper()
	oRem, oHead := ghQueueRemoval, ghPRHead
	t.Cleanup(func() { ghQueueRemoval, ghPRHead = oRem, oHead })
	reads := 0
	ghQueueRemoval = func(string, string, int) (QueueRemoval, error) { reads++; return rem, nil }
	ghPRHead = func(string, string) (*PRHead, error) {
		return &PRHead{Number: 1200, State: "OPEN", HeadSHA: "dead0001"}, nil
	}
	return &reads
}

// A PR queued without --wait and dropped by the queue for failed checks: no
// wait saw it, so the next sync reads the queued-but-unmerged PRs' timelines
// and records the red on the PR's lane, once.
func TestSync_AQueuedPRTheQueueDroppedForFailedChecksIsRecordedRedOnItsLane(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	clone := repoWithOrigin(t)
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Lane: "lane/q", Verdict: "queued",
		Detail: map[string]string{"pr": "1200", "method": "merge queue"}})
	queueDropWorld(t, QueueRemoval{Removed: true, Reason: "failed_checks", FailedChecks: true})

	for range 2 {
		if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	var reds []tdd.Event
	for _, e := range ofKind(emitted(t), "ci") {
		if e.Detail["ci"] == "queue" {
			reds = append(reds, e)
		}
	}
	if len(reds) != 1 || reds[0].Lane != "lane/q" || reds[0].Verdict != "red" || reds[0].Detail["pr"] != "1200" {
		t.Fatalf("queue ci events = %+v, want one red for PR 1200 on lane/q", reds)
	}
}

// A PR still in the queue, dropped for another reason, or already merged by the
// verb is not read or recorded as a red.
func TestSync_AQueuedPRNotDroppedForFailedChecksRecordsNoRed(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	clone := repoWithOrigin(t)
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Lane: "lane/q", Verdict: "queued", Detail: map[string]string{"pr": "1200"}})
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Lane: "lane/m", Verdict: "queued", Detail: map[string]string{"pr": "1201"}})
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Lane: "lane/m", Verdict: "ok", Detail: map[string]string{"pr": "1201"}})
	reads := queueDropWorld(t, QueueRemoval{Removed: true, Reason: "auto-merge disabled"})

	if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if *reads != 1 {
		t.Errorf("timelines read = %d, want 1 (only PR 1200 is queued and unmerged)", *reads)
	}
	for _, e := range ofKind(emitted(t), "ci") {
		t.Fatalf("recorded %+v, want no ci event", e)
	}
}

// A revert's subject is no PR of its own unless GitHub appended its PR number:
// the number inside the reverted title is not the revert's.
func TestPRNumber_ARevertNamesItsOwnPRNotTheRevertedOne(t *testing.T) {
	for subject, want := range map[string]int{
		`Revert "Add the frobnicator (#1089)"`:         0,
		`Revert "Add the frobnicator (#1089)" (#1100)`: 1100,
		`Revert "Merge pull request #12 from o/topic"`: 0,
	} {
		if got := prNumber(subject); got != want {
			t.Errorf("prNumber(%q) = %d, want %d", subject, got, want)
		}
	}
}

// Dropped for failed checks, queued again by hand and merged: the newest queue
// event is the merge, but the lane still had a red queue run.
func TestSync_AQueuedPRDroppedThenRequeuedAndMergedKeepsItsRed(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateState(t)
	clone := repoWithOrigin(t)
	tdd.AppendEvent(tdd.Event{Kind: "merge", Root: clone, Lane: "lane/q", Verdict: "queued",
		Detail: map[string]string{"pr": "1200", "method": "merge queue"}})
	queueDropWorld(t, QueueRemoval{Reason: "merged", FailedChecks: true})

	if err := Sync(clone, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	n := 0
	for _, e := range ofKind(emitted(t), "ci") {
		if e.Detail["ci"] == "queue" && e.Lane == "lane/q" && e.Verdict == "red" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("queue red events = %d, want 1", n)
	}
}
