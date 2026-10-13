package measure

import (
	"reflect"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func mergeEv(sec float64, lane, pr, head string) tdd.Event {
	return ev(sec, lane, "merge", verdictDetail("ok", "pr", pr, "head", head))
}

func noPRBranch(int) (string, bool) { return "", false }

// An escape record is attributed to the lane it was recorded for, keyed by its PR when it
// names one.
func TestAttributeEscapes_ARecordNamesItsLane(t *testing.T) {
	evs := []tdd.Event{
		ev(10, "lane/a", "escape", verdictDetail("escape", "id", "e1")),
		ev(11, "lane/b", "escape", verdictDetail("false-positive", "id", "e2")),
	}
	got := AttributeEscapes(AttributionInput{Events: evs, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "record", Key: "record:e1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v (a false positive is no escape)", got, want)
	}
}

// An escape issue names a PR or a merge sha; the PR maps to the lane that merged it.
func TestAttributeEscapes_AnEscapeIssueMapsItsPRToTheLaneThatMergedIt(t *testing.T) {
	evs := []tdd.Event{mergeEv(5, "lane/a", "481", "abcdef1234567890abcdef1234567890abcdef12")}
	issues := []EscapeIssue{
		{Number: 500, Title: "escape: login 500s after the cart change", Body: "Caused by PR #481."},
		{Number: 501, Title: "escape: wrong total", Body: "Introduced by the merge abcdef1 on main."},
		{Number: 502, Title: "bug: unrelated", Body: "PR #481"},
	}
	got := AttributeEscapes(AttributionInput{Events: evs, Issues: issues, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "issue", Key: "pr:481"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want one escape for lane/a: the two issues name the same PR, and a non-escape title is ignored", got)
	}
}

// A PR no merge event names is mapped through its head branch, read from the host.
func TestAttributeEscapes_APRWithNoMergeEventIsMappedThroughItsHeadBranch(t *testing.T) {
	issues := []EscapeIssue{{Number: 7, Title: "escape: x", Body: "see (#90)"}}
	branch := func(n int) (string, bool) { return "lane/z", n == 90 }
	got := AttributeEscapes(AttributionInput{Issues: issues, PRBranch: branch})
	want := []Attribution{{Lane: "lane/z", Source: "issue", Key: "pr:90"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An issue that names nothing mappable is left unattributed, never guessed.
func TestAttributeEscapes_AnIssueThatNamesNoKnownMergeIsUnattributed(t *testing.T) {
	issues := []EscapeIssue{{Number: 7, Title: "escape: x", Body: "it broke"}, {Number: 8, Title: "escape: y", Body: "PR #77"}}
	if got := AttributeEscapes(AttributionInput{Issues: issues, PRBranch: noPRBranch}); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

// A red run on main is blamed on the merge that produced the commit it ran on: found by its
// head sha among the merges the log holds, else by the PR its commit title names. Another
// lane that merged earlier, or a second red of the same merge, is not blamed.
func TestAttributeEscapes_ARedRunOnMainBlamesTheMergeThatProducedItsCommit(t *testing.T) {
	evs := []tdd.Event{
		ev(100, "lane/a", "merge", verdictDetail("ok", "pr", "1", "head", "aaaaaaa", "sha", "1111111")),
		mergeEv(300, "lane/b", "2", "bbbbbbb"),
		mergeEv(500, "lane/c", "3", "ccccccc"),
	}
	at := func(sec float64) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	reds := []RedRun{
		{At: at(150), SHA: "1111111abcdef", Ref: "r1"},         // the commit lane/a's merge made, by sha
		{At: at(160), SHA: "1111111abcdef", Ref: "r2"},         // a second red of the same merge
		{At: at(550), SHA: "zzzzzzz", Title: "Add c (#3)"},     // no sha known: the title names PR 3
		{At: at(560), SHA: "yyyyyyy", Title: "No pr in title"}, // nothing maps it
	}
	got := AttributeEscapes(AttributionInput{Events: evs, MainReds: reds, PRBranch: noPRBranch})
	want := []Attribution{
		{Lane: "lane/a", Source: "main-red", Key: "pr:1"},
		{Lane: "lane/c", Source: "main-red", Key: "pr:3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A merge gate that blocks is an incoming merge refused, not main going red: lane A merged,
// lane B's pre-merge gate then blocked, and A is not blamed for it.
func TestAttributeEscapes_ABlockedMergeGateIsNotMainGoingRed(t *testing.T) {
	evs := []tdd.Event{
		mergeEv(100, "lane/a", "1", "aaaaaaa"),
		ev(200, "main", "merge_gate_result", func(e *tdd.Event) { e.Verdict = "blocked" }),
		ev(210, "lane/b", "merge_gate_result", func(e *tdd.Event) { e.Verdict = "blocked" }),
	}
	if got := AttributeEscapes(AttributionInput{Events: evs, PRBranch: noPRBranch}); len(got) != 0 {
		t.Errorf("got %+v, want no escape for lane/a", got)
	}
}

// The same escape seen by two sources counts once, by the first source of the table.
func TestAttributeEscapes_AnEscapeSeenByTwoSourcesCountsOnce(t *testing.T) {
	evs := []tdd.Event{
		mergeEv(100, "lane/a", "481", "aaaaaaa"),
		ev(120, "lane/a", "escape", verdictDetail("escape", "id", "e1", "pr", "481")),
	}
	issues := []EscapeIssue{{Number: 9, Title: "escape: x", Body: "PR #481"}}
	reds := []RedRun{{At: base.Add(130 * time.Second), SHA: "zzz", Title: "x (#481)", Ref: "r"}}
	got := AttributeEscapes(AttributionInput{Events: evs, Issues: issues, MainReds: reds, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "record", Key: "pr:481"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An escape recorded from trunk with --pr counts for the lane of that PR, however the log
// came to hold a merge of the same PR logged against trunk (an outside merge).
func TestAttributeEscapes_ATrunkRecordWithAPRCountsForThePRsLaneNotForTrunk(t *testing.T) {
	evs := []tdd.Event{
		ev(10, "main", "merge", verdictDetail("ok", "pr", "5", "sha", "5555555")),
		mergeEv(20, "lane/z", "5", "zzzzzzz"),
		ev(30, "main", "escape", verdictDetail("escape", "id", "e1", "pr", "5")),
	}
	got := AttributeEscapes(AttributionInput{Events: evs, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/z", Source: "record", Key: "pr:5"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// With only the outside merge in the log, the host's head branch names the lane.
	evs = []tdd.Event{ev(10, "main", "merge", verdictDetail("ok", "pr", "5")), evs[2]}
	got = AttributeEscapes(AttributionInput{Events: evs, PRBranch: func(n int) (string, bool) { return "lane/q", n == 5 }})
	want = []Attribution{{Lane: "lane/q", Source: "record", Key: "pr:5"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outside merge only: got %+v, want %+v", got, want)
	}
}

// A record with no PR and the issue it opened are one escape: the issue number the record
// opened is in the log, and the issue is not counted again.
func TestAttributeEscapes_ARecordAndTheIssueItOpenedCountOnce(t *testing.T) {
	evs := []tdd.Event{
		mergeEv(5, "lane/a", "1", "aaaaaaa"),
		ev(10, "lane/a", "escape", verdictDetail("escape", "id", "e1")),
		ev(11, "lane/a", "escape.issue", detail("id", "e1", "number", "500")),
	}
	issues := []EscapeIssue{{Number: 500, Title: "escape: totals wrong", Body: "Introduced by PR #1."}}
	got := AttributeEscapes(AttributionInput{Events: evs, Issues: issues, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "record", Key: "record:e1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want one escape: the record, not the issue it opened as well", got)
	}
}

// A record that names no lane cannot cover its issue: the issue, which names the PR, counts.
func TestAttributeEscapes_AnIssueIsStillCountedWhenItsRecordNamesNoLane(t *testing.T) {
	evs := []tdd.Event{
		mergeEv(5, "lane/a", "1", "aaaaaaa"),
		ev(10, "main", "escape", verdictDetail("escape", "id", "e1")),
		ev(11, "main", "escape.issue", detail("id", "e1", "number", "500")),
	}
	issues := []EscapeIssue{{Number: 500, Title: "escape: totals wrong", Body: "Introduced by PR #1."}}
	got := AttributeEscapes(AttributionInput{Events: evs, Issues: issues, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "issue", Key: "pr:1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// ratchet: test_removed TestAttributeEscapes_TheFirstRedOnMainAfterAMergeBlamesItsLane: replaced by TestAttributeEscapes_ARedRunOnMainBlamesTheMergeThatProducedItsCommit: the first red after a merge blamed whoever merged last, not the merge that made the red commit
// ratchet: test_removed TestMainRedsFromEvents_ABlockedMergeGateOnTrunkIsARedRun: a blocked merge gate refused an incoming merge and is no red run on main (TestAttributeEscapes_ABlockedMergeGateIsNotMainGoingRed)
